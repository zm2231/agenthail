package surfacetest

import "context"

type FakeCredentials struct {
	LiveTokens       map[string]string
	Rotations        int
	UnattachedErrors []error
	Claims           []string
	RotateErr        error
	Expired          bool
}

func (f *FakeCredentials) Current(string) bool { return !f.Expired }

func (f *FakeCredentials) URL() string { return "http://127.0.0.1:7412/mcp" }

func (f *FakeCredentials) Pending(context.Context) (string, string, error) {
	return "ahs_pending", "ref-1", nil
}

func (f *FakeCredentials) Claim(ref, id string) error {
	f.Claims = append(f.Claims, ref+"="+id)
	return nil
}

func (f *FakeCredentials) Live(id string) (string, bool) {
	token, ok := f.LiveTokens[id]
	return token, ok
}

func (f *FakeCredentials) Rotate(id string) (string, error) {
	f.Rotations++
	return "ahs_rotated", f.RotateErr
}

func (f *FakeCredentials) Unattached(_ string, err error) {
	f.UnattachedErrors = append(f.UnattachedErrors, err)
}
