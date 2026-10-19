package registry

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
)

var ErrSigninProfileNotFound = errors.New("no sign-in profile matched")

const signinProfileColumns = `id,label,browser,profile,created_at`

func scanSigninProfile(row interface{ Scan(...any) error }) (signin.Profile, error) {
	var profile signin.Profile
	var browser string
	err := row.Scan(&profile.ID, &profile.Label, &browser, &profile.Profile, &profile.CreatedAt)
	profile.Browser = signin.Browser(browser)
	return profile, err
}

func (r *Registry) ListSigninProfiles() ([]signin.Profile, error) {
	rows, err := r.read.Query(`SELECT ` + signinProfileColumns + ` FROM signin_profiles ORDER BY label`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	profiles := []signin.Profile{}
	for rows.Next() {
		profile, err := scanSigninProfile(rows)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, rows.Err()
}

func (r *Registry) SigninProfile(ref string) (signin.Profile, error) {
	profile, err := scanSigninProfile(r.read.QueryRow(`SELECT `+signinProfileColumns+` FROM signin_profiles WHERE id=? OR label=? ORDER BY id=? DESC LIMIT 1`, ref, ref, ref))
	if errors.Is(err, sql.ErrNoRows) {
		return signin.Profile{}, fmt.Errorf("%w %q", ErrSigninProfileNotFound, ref)
	}
	return profile, err
}

func (r *Registry) AddSigninProfile(label string, browser signin.Browser, selector string) (signin.Profile, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return signin.Profile{}, fmt.Errorf("a sign-in profile needs a label")
	}
	if strings.HasPrefix(label, "sp_") {
		return signin.Profile{}, fmt.Errorf("label %q is reserved for profile IDs", label)
	}
	if label == "none" {
		return signin.Profile{}, fmt.Errorf("label %q is reserved", label)
	}
	if !browser.Valid() {
		return signin.Profile{}, fmt.Errorf("unsupported browser %q", browser)
	}
	if strings.TrimSpace(selector) == "" {
		return signin.Profile{}, fmt.Errorf("a sign-in profile needs a browser profile")
	}
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return signin.Profile{}, fmt.Errorf("generate profile id: %w", err)
	}
	profile := signin.Profile{ID: "sp_" + hex.EncodeToString(raw), Label: label, Browser: browser, Profile: selector, CreatedAt: r.now().UTC().Format(time.RFC3339)}
	var labelTaken, sourceTaken string
	_ = r.read.QueryRow(`SELECT id FROM signin_profiles WHERE label=?`, label).Scan(&labelTaken)
	if labelTaken != "" {
		return signin.Profile{}, fmt.Errorf("a sign-in profile is already labelled %q", label)
	}
	_ = r.read.QueryRow(`SELECT label FROM signin_profiles WHERE browser=? AND profile=?`, string(browser), selector).Scan(&sourceTaken)
	if sourceTaken != "" {
		return signin.Profile{}, fmt.Errorf("%s profile %q is already added as %q", browser.Label(), selector, sourceTaken)
	}
	if _, err := r.db.Exec(`INSERT INTO signin_profiles(`+signinProfileColumns+`) VALUES(?,?,?,?,?)`, profile.ID, profile.Label, string(profile.Browser), profile.Profile, profile.CreatedAt); err != nil {
		return signin.Profile{}, err
	}
	return profile, nil
}

// RemoveSigninProfile deletes a profile. Consumers bound to it become
// unconfigured rather than moving to another profile.
func (r *Registry) RemoveSigninProfile(ref string) (signin.Profile, error) {
	profile, err := r.SigninProfile(ref)
	if err != nil {
		return signin.Profile{}, err
	}
	if _, err := r.db.Exec(`DELETE FROM signin_profiles WHERE id=?`, profile.ID); err != nil {
		return signin.Profile{}, err
	}
	return profile, nil
}

func (r *Registry) SigninBindings() (map[signin.Consumer]string, error) {
	rows, err := r.read.Query(`SELECT consumer,profile_id FROM signin_bindings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := map[signin.Consumer]string{}
	for rows.Next() {
		var consumer, profileID string
		if err := rows.Scan(&consumer, &profileID); err != nil {
			return nil, err
		}
		bindings[signin.Consumer(consumer)] = profileID
	}
	return bindings, rows.Err()
}

// BindSignin points a consumer at a profile, or clears it when ref is empty.
func (r *Registry) BindSignin(consumer signin.Consumer, ref string) (*signin.Profile, error) {
	if _, ok := signin.LookupConsumer(string(consumer)); !ok {
		return nil, fmt.Errorf("unknown sign-in consumer %q", consumer)
	}
	if ref == "" {
		_, err := r.db.Exec(`DELETE FROM signin_bindings WHERE consumer=?`, string(consumer))
		return nil, err
	}
	profile, err := r.SigninProfile(ref)
	if err != nil {
		return nil, err
	}
	if _, err := r.db.Exec(`INSERT INTO signin_bindings(consumer,profile_id) VALUES(?,?) ON CONFLICT(consumer) DO UPDATE SET profile_id=excluded.profile_id`, string(consumer), profile.ID); err != nil {
		return nil, err
	}
	return &profile, nil
}

func (r *Registry) ResolveSignin(target signin.Target) (signin.Source, error) {
	profile, err := scanSigninProfile(r.read.QueryRow(`SELECT p.id,p.label,p.browser,p.profile,p.created_at FROM signin_bindings b JOIN signin_profiles p ON p.id=b.profile_id WHERE b.consumer=?`, string(target.Consumer)))
	if errors.Is(err, sql.ErrNoRows) {
		return signin.Source{}, signin.NotConfiguredError{Consumer: target.Consumer}
	}
	if err != nil {
		return signin.Source{}, fmt.Errorf("read %s sign-in profile: %w", target.Consumer.Label(), err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return signin.Source{}, err
	}
	return signin.SourceFor(home, profile)
}
