package kubectl

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
)

func Kubectl(command ...string) (stdout, stderr string, err error) {
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd := exec.Command("./cluster/kubectl.sh", command...)
	cmd.Dir = getClusterRootDirectory()
	cmd.Stderr = &stderrBuf
	cmd.Stdout = &stdoutBuf
	err = cmd.Run()
	stdout = stdoutBuf.String()
	stderr = stderrBuf.String()
	return
}

func getClusterRootDirectory() string {
	dir, found := os.LookupEnv("CLUSTER_ROOT_DIRECTORY")
	if !found {
		return ".."
	}
	return dir
}

// StartPortForwardCommand starts a port-forward command in the background and returns the process
func StartPortForwardCommand(namespace, podName string, sourcePort, targetPort int) (*exec.Cmd, error) {
	// #nosec G204 -- test code with controlled inputs
	cmd := exec.Command("./cluster/kubectl.sh", "port-forward", "-n", namespace,
		fmt.Sprintf("pod/%s", podName), fmt.Sprintf("%d:%d", sourcePort, targetPort))
	cmd.Dir = getClusterRootDirectory()

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start port-forward: %w", err)
	}
	return cmd, nil
}

// KillPortForwardCommand kills the port-forward process
func KillPortForwardCommand(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
