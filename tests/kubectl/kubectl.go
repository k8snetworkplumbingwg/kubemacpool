package kubectl

import (
	"bytes"
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
