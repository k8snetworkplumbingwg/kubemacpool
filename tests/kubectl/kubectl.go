package kubectl

import (
	"bytes"
	"os"
	"os/exec"
)

func Kubectl(command ...string) (string, string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("./cluster/kubectl.sh", command...)
	cmd.Dir = getClusterRootDirectory()
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func getClusterRootDirectory() string {
	dir, found := os.LookupEnv("CLUSTER_ROOT_DIRECTORY")
	if !found {
		return ".."
	}
	return dir
}
