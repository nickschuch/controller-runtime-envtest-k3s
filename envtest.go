package envtest

import (
	"fmt"
	"time"

	"k8s.io/client-go/rest"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"golang.org/x/net/context"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	crenvtest "sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	// DefaultImage which will be used if non is provided.
	DefaultImage = "docker.io/rancher/k3s:v0.4.0"
	// InsecurePort assigned to the K3s cluster.
	InsecurePort = "8080"
	// BindAddress assigned to the K3s cluster.
	BindAddress = "0.0.0.0"
)

// Environment which will back the Kubebuilder testsuite.
type Environment struct {
	// Image which will be used for spinning up K3s.
	Image string
	// CRDDirectoryPaths for preloading CustomResourceDefinitions.
	// This is field name was taken from the controller-runtime envtest package
	// for compatibility reasons.
	CRDDirectoryPaths []string
	// Internal container identifier.
	id string
}

// Start the test environment.
func (e *Environment) Start() (*rest.Config, error) {
	ctx := context.Background()

	if e.Image == "" {
		e.Image = DefaultImage
	}

	cli, err := client.NewEnvClient()
	if err != nil {
		return nil, err
	}

	_, err = cli.ImagePull(ctx, e.Image, types.ImagePullOptions{})
	if err != nil {
		return nil, err
	}

	natPort, err := nat.NewPort("tcp", InsecurePort)
	if err != nil {
		return nil, err
	}

	containerConfig := &container.Config{
		Image: e.Image,
		Cmd: []string{
			"server",
			"--kube-apiserver-arg", fmt.Sprintf("insecure-port=%s", InsecurePort),
			"--kube-apiserver-arg", fmt.Sprintf("insecure-bind-address=%s", BindAddress),
		},
		ExposedPorts: nat.PortSet{
			natPort: {},
		},
	}

	containerHostConfig := &container.HostConfig{
		Privileged: true,
		PortBindings: map[nat.Port][]nat.PortBinding{
			natPort: []nat.PortBinding{
				{
					HostIP: BindAddress,
				},
			},
		},
	}

	resp, err := cli.ContainerCreate(ctx, containerConfig, containerHostConfig, nil, "")
	if err != nil {
		return nil, err
	}

	e.id = resp.ID

	err = cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
	if err != nil {
		return nil, err
	}

	inspect, err := cli.ContainerInspect(ctx, resp.ID)
	if err != nil {
		return nil, err
	}

	port, err := getContainerPort(inspect.NetworkSettings.Ports, natPort)
	if err != nil {
		return nil, err
	}

	host := fmt.Sprintf("http://localhost:%s", port)

	ready := waitForCluster(fmt.Sprintf("%s/healthz", host), 20, time.Second*5)
	if !ready {
		return nil, fmt.Errorf("cluster did not become available")
	}

	config := &rest.Config{
		Host: host,
	}

	crenvtest.InstallCRDs(config, envtest.CRDInstallOptions{
		Paths: e.CRDDirectoryPaths,
	})

	return config, nil
}

// Stop the test environment.
func (e *Environment) Stop() error {
	ctx := context.Background()

	cli, err := client.NewEnvClient()
	if err != nil {
		return err
	}

	fmt.Println("removing container:", e.id)

	err = cli.ContainerStop(ctx, e.id, nil)
	if err != nil {
		return err
	}

	return cli.ContainerRemove(ctx, e.id, types.ContainerRemoveOptions{
		RemoveVolumes: true,
		RemoveLinks:   true,
		Force:         true,
	})

}
