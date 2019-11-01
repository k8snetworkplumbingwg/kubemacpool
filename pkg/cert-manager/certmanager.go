/*
Copyright 2019 The KubeMacPool Authors.

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

package certmanager

import (
	"github.com/go-logr/logr"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"time"
)

var log logr.Logger

func init() {
	log = logf.Log.WithName("manager")
}

func NotifyAboutNearExpiration(cert *x509.Certificate, maxTimeLeft time.Duration, notificationChannel chan struct{}, cancelChannel chan struct{}) {
	log.Info("XXX1")
	deadline := cert.NotAfter.Add(-maxTimeLeft)
	log.Info("XXX2")
	timer := time.NewTimer(time.Until(deadline))
	log.Info("XXX3")
	select {
	case <-timer.C:
		log.Info("XXX4")
		close(notificationChannel)
	case <-cancelChannel:
		log.Info("XXX5")
		return
	}
}

func Read(certPEMPath string) (*x509.Certificate, error) {
	log.Info("XXX6")
	certPEM, err := ioutil.ReadFile(certPEMPath)
	if err != nil {
		return nil, fmt.Errorf("failed reading cert file: %v", err)
	}

	log.Info("XXX7")
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return nil, fmt.Errorf("failed to parse certificate PEM")
	}

	log.Info("XXX8")
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %v", err)
	}

	log.Info("XXX9")
	return cert, nil
}
