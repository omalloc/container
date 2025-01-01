package v1

import "io"

type Container interface {
	io.Closer

	ImagePull(imageName string) error
	CreateContainer(opt *ContainerOpts) error
	RemoveContainer(opt *ContainerOpts) error
}
