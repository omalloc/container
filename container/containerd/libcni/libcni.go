package libcni

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"

	containerd "github.com/containerd/containerd/v2/client"
	gocni "github.com/containerd/go-cni"
	"github.com/pkg/errors"
)

func Initialize() (gocni.CNI, error) {
	log.Printf("Writing network config...\n")
	if !dirExists(CNIConfDir) {
		if err := os.MkdirAll(CNIConfDir, 0755); err != nil {
			return nil, fmt.Errorf("cannot create directory: %s", CNIConfDir)
		}
	}

	netConfig := path.Join(CNIConfDir, defaultCNIConfFilename)
	if err := os.WriteFile(netConfig, []byte(defaultCNIConf), 644); err != nil {
		return nil, fmt.Errorf("cannot write network config: %s", defaultCNIConfFilename)
	}

	cni, err := gocni.New(
		gocni.WithPluginConfDir(CNIConfDir),
		gocni.WithPluginDir([]string{CNIBinDir}),
		gocni.WithInterfacePrefix(defaultIfPrefix),
	)
	if err != nil {
		return nil, fmt.Errorf("error initializing cni: %s", err)
	}

	// Load the cni configuration
	if err := cni.Load(gocni.WithLoNetwork, gocni.WithConfListFile(filepath.Join(CNIConfDir, defaultCNIConfFilename))); err != nil {
		return nil, fmt.Errorf("failed to load cni configuration: %v", err)
	}

	return cni, nil
}

// CreateCNINetwork creates a CNI network interface and attaches it to the context
func CreateCNINetwork(ctx context.Context, cni gocni.CNI, task containerd.Task, labels map[string]string) (*gocni.Result, error) {
	id := netID(task)
	netns := netNamespace(task)
	result, err := cni.Setup(ctx, id, netns, gocni.WithLabels(labels))
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to setup network for task %q: %v", id, err)
	}

	return result, nil
}

// DeleteCNINetwork deletes a CNI network based on task ID and Pid
func DeleteCNINetwork(ctx context.Context, cni gocni.CNI, client *containerd.Client, name string) error {
	container, containerErr := client.LoadContainer(ctx, name)
	if containerErr == nil {
		task, err := container.Task(ctx, nil)
		if err != nil {
			log.Printf("[Delete] unable to find task for container: %s\n", name)
			return nil
		}

		log.Printf("[Delete] removing CNI network for: %s\n", task.ID())

		id := netID(task)
		netns := netNamespace(task)

		if err := cni.Remove(ctx, id, netns); err != nil {
			return errors.Wrapf(err, "Failed to remove network for task: %q, %v", id, err)
		}
		log.Printf("[Delete] removed: %s from namespace: %s, ID: %s\n", name, netns, id)

		return nil
	}

	return errors.Wrapf(containerErr, "Unable to find container: %s, error: %s", name, containerErr)
}

// GetIPAddress returns the IP address from container based on container name and PID
func GetIPAddress(container string, PID uint32) (string, error) {
	CNIDir := path.Join(CNIDataDir, defaultNetworkName)

	files, err := os.ReadDir(CNIDir)
	if err != nil {
		return "", fmt.Errorf("failed to read CNI dir for container %s: %v", container, err)
	}

	for _, file := range files {
		// each fileName is an IP address
		fileName := file.Name()

		resultsFile := filepath.Join(CNIDir, fileName)
		found, err := isCNIResultForPID(resultsFile, container, PID)
		if err != nil {
			return "", err
		}

		if found {
			return fileName, nil
		}
	}

	return "", fmt.Errorf("unable to get IP address for container: %s", container)
}

// isCNIResultForPID confirms if the CNI result file contains the
// process name, PID and interface name
//
// Example:
//
// /var/run/cni/openfaas-cni-bridge/10.62.0.2
//
// nats-621
// eth1
func isCNIResultForPID(fileName, container string, PID uint32) (bool, error) {
	found := false

	f, err := os.Open(fileName)
	if err != nil {
		return false, fmt.Errorf("failed to open CNI IP file for %s: %v", fileName, err)
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	processLine, _ := reader.ReadString('\n')
	if strings.Contains(processLine, fmt.Sprintf("%s-%d", container, PID)) {
		ethNameLine, _ := reader.ReadString('\n')
		if strings.Contains(ethNameLine, defaultIfPrefix) {
			found = true
		}
	}

	return found, nil
}

// CNIGateway returns the gateway for default subnet
func CNIGateway() (string, error) {
	ip, _, err := net.ParseCIDR(defaultSubnet)
	if err != nil {
		return "", fmt.Errorf("error formatting gateway for network %s", defaultSubnet)
	}
	ip = ip.To4()
	ip[3] = 1
	return ip.String(), nil
}

// netID generates the network IF based on task name and task PID
func netID(task containerd.Task) string {
	return fmt.Sprintf("%s-%d", task.ID(), task.Pid())
}

// netNamespace generates the namespace path based on task PID.
func netNamespace(task containerd.Task) string {
	return fmt.Sprintf(NetNSPathFmt, task.Pid())
}

func dirEmpty(dirname string) (isEmpty bool) {
	if !dirExists(dirname) {
		return
	}

	f, err := os.Open(dirname)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	// If the first file is EOF, the directory is empty
	if _, err = f.Readdir(1); err == io.EOF {
		isEmpty = true
	}
	return isEmpty
}

func dirExists(dirname string) bool {
	exists, info := pathExists(dirname)
	if !exists {
		return false
	}

	return info.IsDir()
}

func pathExists(path string) (bool, os.FileInfo) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}

	return true, info
}
