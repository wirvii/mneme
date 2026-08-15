package cli

import (
	"strings"
	"testing"

	"github.com/wirvii/mneme/internal/runtimecompat"
)

func TestRuntimeInstallNotice(t *testing.T) {
	tests := []struct {
		name       string
		status     runtimecompat.Status
		wantErr    bool
		wantNotice string
	}{
		{name: "absent", status: runtimecompat.Status{Command: "codex"}},
		{name: "below stable", status: runtimecompat.Status{Command: "codex", Installed: true, Version: "0.146.9", Minimum: "0.147.0"}, wantErr: true},
		{name: "stable degraded", status: runtimecompat.Status{Command: "codex", Installed: true, Version: "0.147.0", Minimum: "0.147.0", Supported: true, CapabilityMinimum: "0.148.0-alpha.19"}, wantNotice: "native multi-agent delegation"},
		{name: "fully supported", status: runtimecompat.Status{Command: "codex", Installed: true, Version: "0.148.0-alpha.19", Minimum: "0.147.0", Supported: true, CapabilityMinimum: "0.148.0-alpha.19", FullySupported: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notice, err := runtimeInstallNotice(tt.status)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !strings.Contains(notice, tt.wantNotice) {
				t.Fatalf("notice = %q, want substring %q", notice, tt.wantNotice)
			}
			if strings.Contains(strings.ToLower(notice), "install alpha") {
				t.Fatalf("notice recommends alpha: %q", notice)
			}
		})
	}
}
