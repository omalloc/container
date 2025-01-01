package containerd

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"syscall"

	"github.com/containerd/containerd/api/services/tasks/v1"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oci"
	gocni "github.com/containerd/go-cni"
	v1 "github.com/omalloc/container/api/v1"
	"github.com/opencontainers/runtime-spec/specs-go"
)

type Containerd struct {
	client *containerd.Client
}

func New() (v1.Container, error) {
	client, err := containerd.New("/run/containerd/containerd.sock")
	if err != nil {
		return nil, err
	}

	c := &Containerd{
		client: client,
	}

	if c.ping() != nil {
		return nil, err
	}

	return c, nil
}

func (c *Containerd) CreateContainer(opt *v1.ContainerOpts) error {
	imageName := imageNameJoin(opt.Image, opt.Tag)
	log.Printf("Trying load image %s", imageName)
	image, err := c.imageLoad(imageName, true)
	if err != nil {
		return err
	}

	ctx := withCtx()
	snapname := fmt.Sprintf("%s-snapshot", opt.Name)
	container, err := c.client.NewContainer(
		ctx,
		opt.Name,
		containerd.WithNewSnapshot(snapname, image),
		containerd.WithNewSpec(
			oci.WithImageConfig(image),
			oci.WithHostNamespace(specs.PIDNamespace),
			oci.WithProcessArgs("top"),
		),
	)
	if err != nil {
		return err
	}
	log.Printf("Successfully created container with ID %q and snapshot with ID %q", container.ID(), snapname)

	libnetwork, err := gocni.New(
		// one for loopback network interface
		gocni.WithMinNetworkCount(2),
		gocni.WithPluginConfDir("/etc/cni/net.d"),
		gocni.WithPluginDir([]string{"/opt/cni/bin"}),
		gocni.WithInterfacePrefix("eth"),
	)
	if err != nil {
		log.Fatalf("failed to initialize cni library: %v", err)
	}

	if err := libnetwork.Load(gocni.WithLoNetwork, gocni.WithDefaultConf); err != nil {
		log.Fatalf("failed to load cni configuration: %v", err)
	}

	labels := map[string]string{
		"omalloc_pod_namespace":          "omalloc",
		"omalloc_pod_name":               opt.Name,
		"omalloc_pod_infra_container_id": container.ID(),
		"IgnoreUnknown":                  "1",
	}

	// Setup network
	result, err := libnetwork.Setup(withCtx(), container.ID(), libnetworkNamespace(opt.Name), gocni.WithLabels(labels))
	if err != nil {
		log.Fatalf("failed to setup network for namespace: %v", err)
	}
	// Get IP of the default interface
	IP := result.Interfaces["eth0"].IPConfigs[0].IP.String()
	fmt.Printf("IP of the default interface %s:%s", "eth0", IP)

	task, err := container.NewTask(ctx, cio.NewCreator(cio.WithStdio))
	if err != nil {
		return err
	}

	return task.Start(ctx)
}

func (c *Containerd) RemoveContainer(opt *v1.ContainerOpts) error {
	ctx := withCtx()
	svc := c.client.ContainerService()

	// libnetwork, err := gocni.New(
	// 	// one for loopback network interface
	// 	gocni.WithMinNetworkCount(2),
	// 	gocni.WithPluginConfDir("/etc/cni/net.d"),
	// 	gocni.WithPluginDir([]string{"/opt/cni/bin"}),
	// 	gocni.WithInterfacePrefix("eth"),
	// )
	// if err != nil {
	// 	log.Fatalf("failed to initialize cni library: %v", err)
	// }
	// libnetwork.Remove(ctx, )

	container, err := svc.Get(ctx, opt.Name)
	if err != nil {
		return err
	}

	_, err = c.client.TaskService().Kill(ctx, &tasks.KillRequest{
		ContainerID: container.ID,
		Signal:      uint32(syscall.SIGTERM),
	})
	if err != nil {
		if !strings.Contains(err.Error(), "NotFound") {
			return err
		}
	}

	_, err = c.client.TaskService().Delete(ctx, &tasks.DeleteTaskRequest{
		ContainerID: container.ID,
	})
	if err != nil {
		if !strings.Contains(err.Error(), "NotFound") {
			return err
		}
	}

	if err := c.client.SnapshotService(container.Snapshotter).Remove(ctx, container.SnapshotKey); err != nil {
		return err
	}

	return svc.Delete(ctx, opt.Name)
}

func (c *Containerd) ImagePull(imageName string) error {
	image, err := c.client.Pull(withCtx(), imageName, containerd.WithPullUnpack)
	if err != nil {
		return err
	}

	log.Printf("Successfully pulled %s image\n", image.Name())
	return nil
}

func (c *Containerd) Close() error {
	return c.client.Close()
}

func (c *Containerd) imageLoad(imageName string, pull bool) (containerd.Image, error) {
	image, err := c.client.GetImage(withCtx(), imageName)
	if err != nil {
		if !strings.Contains(err.Error(), "not found") {
			return nil, err
		}

		if !pull {
			return nil, err
		}
		// try pull image
		if err1 := c.ImagePull(imageName); err1 != nil {
			return nil, err1
		}
		// reload image
		image, err = c.client.GetImage(withCtx(), imageName)
	}
	return image, err
}

func (c *Containerd) ping() error {
	version, err := c.client.Version(withCtx())
	if err != nil {
		return err
	}

	log.Printf("Container version %s rev %s\n", version.Version, version.Revision)
	return nil
}

func withCtx() context.Context {
	return namespaces.WithNamespace(context.Background(), "omalloc")
}

func imageNameJoin(name, tag string) string {
	if strings.Index(name, "/") <= -1 {
		name = "docker.io/library/" + name
	}

	if tag == "" {
		return name
	}

	if strings.Contains(name, ":") {
		return name
	}

	return fmt.Sprintf("%s:%s", name, tag)
}

func libnetworkNamespace(name string) string {
	_ = os.MkdirAll("/var/lib/libnetwork/", os.ModeDir)
	return "/var/lib/libnetwork"
}
