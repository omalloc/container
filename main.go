package main

import (
	"fmt"
	"log"
	"os"

	v1 "github.com/omalloc/container/api/v1"
	"github.com/omalloc/container/container/containerd"
)

func main() {
	log.SetPrefix(fmt.Sprintf("container(%d) ", os.Getgid()))

	cli, err := containerd.New()
	if err != nil {
		panic(err)
	}
	defer cli.Close()

	opt := &v1.ContainerOpts{
		Name:  "myapp",
		Image: "redis",
		Tag:   "alpine",
	}

	if err := cli.RemoveContainer(opt); err != nil {
		log.Printf("Remove old container failed %s\n", err)
	}

	if err := cli.CreateContainer(opt); err != nil {
		log.Fatalf("CreateContainer failed %s\n", err)
	}
}
