package daemonapi

const IdentitySettingsPath = "/api/v1/identity-settings"

type IdentitySettingsRequest struct {
	Identity string `json:"identity"`
	SendMode string `json:"sendMode"`
}

type IdentitySettingsResult struct {
	Identity string `json:"identity"`
	SendMode string `json:"sendMode"`
}
