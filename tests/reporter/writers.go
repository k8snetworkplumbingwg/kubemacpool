/*
Copyright 2025 The KubeMacPool Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package reporter

import (
	"fmt"
	"os"
)

func LogToFile(topic, logBody, artifactDir string, failureCount int) error {
	fileName := fmt.Sprintf(artifactDir+"%d_%s.log", failureCount, topic)
	file, err := os.Create(fileName)
	if err != nil {
		return fmt.Errorf("error creating log file %v, err %w", fileName, err)
	}
	defer file.Close()

	if _, err = fmt.Fprint(file, logBody); err != nil {
		fmt.Printf("error writing log %s to file, err %v", fileName, err)
	}
	return nil
}
