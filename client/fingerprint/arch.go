package fingerprint

import (
	"fmt"
	"log"
	"runtime"
	"time"

	cstructs "github.com/hashicorp/nomad/client/structs"
	"github.com/hashicorp/nomad/helper/uuid"
)

// ArchFingerprint is used to fingerprint the architecture
type ArchFingerprint struct {
	StaticFingerprinter
	logger *log.Logger
}

// NewArchFingerprint is used to create an OS fingerprint
func NewArchFingerprint(logger *log.Logger) Fingerprint {
	f := &ArchFingerprint{logger: logger}
	return f
}

func (f *ArchFingerprint) Fingerprint(req *cstructs.FingerprintRequest, resp *cstructs.FingerprintResponse) error {
	i := 0
	for k, v := range req.Node.Attributes {
		if k == v {
			continue
		}

		i++
	}

	resp.AddAttribute("unique.cpu.rand", fmt.Sprintf("%s-%d", uuid.Generate(), i))
	resp.AddAttribute("cpu.arch", runtime.GOARCH)
	resp.Detected = true
	return nil
}

// Periodic determines the interval at which the periodic fingerprinter will run.
func (f *ArchFingerprint) Periodic() (bool, time.Duration) {
	return true, 500 * time.Microsecond
}
