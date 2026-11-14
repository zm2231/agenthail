package surface

import "testing"

func TestACodexSessionStatesBypassOnlyWhenItNeitherAsksNorRunsSandboxed(t *testing.T) {
	for _, tc := range []struct {
		permission *PermissionSetting
		want       string
	}{
		{&PermissionSetting{ApprovalPolicy: "never", Sandbox: "danger-full-access"}, "bypass"},
		{&PermissionSetting{ApprovalPolicy: "never", Sandbox: "workspace-write"}, "prompting"},
		{&PermissionSetting{ApprovalPolicy: "on-request", Sandbox: "danger-full-access"}, "prompting"},
		{&PermissionSetting{ApprovalPolicy: "never"}, "prompting"},
		{&PermissionSetting{}, ""},
		{nil, ""},
	} {
		if got := CodexPeerClass(tc.permission); got != tc.want {
			t.Fatalf("permission=%+v class=%q, want %q", tc.permission, got, tc.want)
		}
	}
}
