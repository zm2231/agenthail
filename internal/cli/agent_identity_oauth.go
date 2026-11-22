package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/mcpoauth"
	"github.com/zm2231/agenthail/internal/registry"
)

const identityOAuthUsage = "usage: agenthail identity oauth [set --url <public origin> | off | list | revoke <grant>] [--json]"

type oauthStatus struct {
	Issuer string                `json:"issuer,omitempty"`
	Grants []registry.OAuthGrant `json:"grants"`
}

func oauthStatusOf(reg *registry.Registry) (oauthStatus, error) {
	status := oauthStatus{Grants: []registry.OAuthGrant{}}
	issuer, on, err := reg.OAuthIssuer()
	if err != nil || !on {
		return status, err
	}
	status.Issuer = issuer
	status.Grants, err = reg.OAuthGrants()
	return status, err
}

func identityOAuth(reg *registry.Registry, positional, args []string, jsonOut bool) error {
	action := "list"
	if len(positional) > 1 {
		action = positional[1]
	}
	switch {
	case action == "set" && len(positional) == 2:
		issuer, err := mcpoauth.ParseIssuer(flagVal(args, "--url"))
		if err != nil {
			return fmt.Errorf("--url: %w", err)
		}
		if err := reg.SetOAuthIssuer(issuer); err != nil {
			return err
		}
	case action == "off" && len(positional) == 2:
		if err := reg.ClearOAuthIssuer(); err != nil {
			return err
		}
	case action == "revoke" && len(positional) == 3:
		grant, err := reg.RevokeOAuthGrant(positional[2])
		if err != nil {
			return err
		}
		if err := reg.RecordHistory(registry.HistoryEntry{Kind: "mcp-oauth", SourceSessionID: grant.IdentityID, Message: "revoke grant " + grant.ID + " of @" + grant.Identity + " to " + grant.ClientName, Result: "revoked from the CLI"}); err != nil {
			return err
		}
		if jsonOut {
			return emit(map[string]any{"revoked": grant})
		}
		fmt.Printf("revoked %s's grant to %s; its next call is refused\n", "@"+grant.Identity, grant.ClientName)
		return nil
	case action != "list" || len(positional) > 2:
		return errors.New(identityOAuthUsage)
	}
	status, err := oauthStatusOf(reg)
	if err != nil {
		return err
	}
	if jsonOut {
		return emit(status)
	}
	fmt.Println(status)
	return nil
}

func (s oauthStatus) String() string {
	if s.Issuer == "" {
		return "OAuth: off. Turn it on with: agenthail identity oauth set --url https://<public host>"
	}
	text := fmt.Sprintf("OAuth: on at %s. Give a client %s; you approve each one on your devices.", s.Issuer, mcpoauth.Resource(s.Issuer))
	for _, grant := range s.Grants {
		text += fmt.Sprintf("\n  %s  @%s to %s, since %s, last seen %s", grant.ID, grant.Identity, grant.ClientName, grant.CreatedAt.Local().Format(time.DateTime), lastSeen(grant.LastSeen))
	}
	return text
}
