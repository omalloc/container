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
	"github.com/containerd/go-cni"
	"github.com/opencontainers/runtime-spec/specs-go"

	v1 "github.com/omalloc/container/api/v1"
	"github.com/omalloc/container/pkg/idgen"
)

type Containerd struct {
	client *containerd.Client
}

func New() (v1.Container, error) {
	client, err := containerd.New("/Users/sendya/.colima/default/containerd.sock")
	if err != nil {
		return nil, err
	}

	_ = os.MkdirAll("/var/lib/libnetwork/", os.ModeDir)

	c := &Containerd{
		client: client,
	}

	if err := c.ping(); err != nil {
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
		idgen.GenerateID(),
		containerd.WithNewSnapshot(snapname, image),
		containerd.WithNewSpec(
			oci.WithImageConfig(image),
			oci.WithHostNamespace(specs.PIDNamespace),
			oci.WithProcessArgs("top"),
		),
		containerd.WithContainerLabels(map[string]string{
			"Names": opt.Name, // 将 NAMES 添加为标签
		}),
	)
	if err != nil {
		log.Fatal(err)
		return err
	}
	log.Printf("Successfully created container with ID %q and snapshot with ID %q", container.ID(), snapname)

	task, err := container.NewTask(ctx, cio.NewCreator(cio.WithStdio))
	if err != nil {
		return err
	}

	libnetwork, err := cni.New(
		// one for loopback network interface
		cni.WithMinNetworkCount(1),
		cni.WithPluginConfDir("/etc/cni/net.d"),
		cni.WithPluginDir([]string{"/opt/cni/bin"}),
		cni.WithInterfacePrefix("eth"),
	)
	if err != nil {
		log.Fatalf("failed to initialize cni library: %v", err)
		return err
	}

	netns, id := libnetworkNamespace(container.ID())

	if err := libnetwork.Load(cni.WithConfListFile("/etc/cni/net.d/10-sbridge.conf")); err != nil {
		log.Fatalf("failed to load cni network: %v", err)
		return err
	}

	result, err := libnetwork.Setup(ctx, id, netns)
	if err != nil {
		log.Fatalf("failed to setup network for namespace: %v", err)
	}
	log.Printf("Successfully setup network for namespace: %s", netns)

	for key, iff := range result.Interfaces {
		if len(iff.IPConfigs) > 0 {
			IP := iff.IPConfigs[0].IP.String()
			fmt.Printf("IP of the interface %s:%s\n", key, IP)
		}
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

func libnetworkNamespace(containerID string) (string, string) {
	id := containerID[:10]
	return fmt.Sprintf("/var/run/netns/%s", id), id
}
