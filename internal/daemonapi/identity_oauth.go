package daemonapi

const IdentityOAuthPath = "/api/v1/identity-oauth"

type IdentityOAuthRequest struct {
	Identity string `json:"identity"`
	Enabled  bool   `json:"enabled"`
}

type IdentityOAuthResult struct {
	Identity string `json:"identity"`
	Enabled  bool   `json:"enabled"`
	Revoked  int    `json:"revoked"`
}
