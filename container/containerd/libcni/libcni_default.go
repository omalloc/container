package libcni

import "fmt"

const (
	// CNIBinDir describes the directory where the CNI binaries are stored
	CNIBinDir = "/opt/cni/bin"

	// CNIConfDir describes the directory where the CNI plugin's configuration is stored
	CNIConfDir = "/etc/cni/net.d"

	// NetNSPathFmt gives the path to the a process network namespace, given the pid
	NetNSPathFmt = "/proc/%d/ns/net"

	// CNIDataDir is the directory CNI stores allocated IP for containers
	CNIDataDir = "/var/run/cni"

	// defaultCNIConfFilename is the vanity filename of default CNI configuration file
	defaultCNIConfFilename = "10-openfaas.conflist"

	// defaultNetworkName names the "docker-bridge"-like CNI plugin-chain installed when no other CNI configuration is present.
	// This value appears in iptables comments created by CNI.
	defaultNetworkName = "openfaas-cni-bridge"

	// defaultBridgeName is the default bridge device name used in the defaultCNIConf
	defaultBridgeName = "openfaas0"

	// defaultSubnet is the default subnet used in the defaultCNIConf -- this value is set to not collide with common container networking subnets:
	defaultSubnet = "10.62.0.0/16"

	// defaultIfPrefix is the interface name to be created in the container
	defaultIfPrefix = "eth"
)

// defaultCNIConf is a CNI configuration that enables network access to containers (docker-bridge style)
var defaultCNIConf = fmt.Sprintf(`
{
    "cniVersion": "0.4.0",
    "name": "%s",
    "plugins": [
      {
        "type": "bridge",
        "bridge": "%s",
        "isGateway": true,
        "ipMasq": true,
        "ipam": {
            "type": "host-local",
            "subnet": "%s",
            "dataDir": "%s",
            "routes": [
                { "dst": "0.0.0.0/0" }
            ]
        }
      },
      {
        "type": "firewall"
      }
    ]
}
`, defaultNetworkName, defaultBridgeName, defaultSubnet, CNIDataDir)
