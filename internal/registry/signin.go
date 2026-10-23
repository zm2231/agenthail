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

func scanSigninProfile(row rowScanner) (signin.Profile, error) {
	var profile signin.Profile
	var browser string
	err := row.Scan(&profile.ID, &profile.Label, &browser, &profile.Profile, &profile.CreatedAt)
	profile.Browser = signin.Browser(browser)
	return profile, err
}

func (r *Registry) ListSigninProfiles() ([]signin.Profile, error) {
	return queryRows(r.read, []signin.Profile{}, scanSigninProfile, `SELECT `+signinProfileColumns+` FROM signin_profiles ORDER BY label`)
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
	if label == signin.RefOff || label == signin.RefAutomatic {
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

// RemoveSigninProfile deletes a profile. Consumers bound to it go back to
// the system default browser rather than moving to another profile.
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

func (r *Registry) SigninChoices() (map[signin.Consumer]signin.Choice, error) {
	choices := map[signin.Consumer]signin.Choice{}
	for _, consumer := range signin.Consumers {
		choice, err := r.SigninChoice(consumer.ID)
		if err != nil {
			return nil, err
		}
		choices[consumer.ID] = choice
	}
	return choices, nil
}

func (r *Registry) SigninChoice(consumer signin.Consumer) (signin.Choice, error) {
	var off int
	if err := r.read.QueryRow(`SELECT COUNT(*) FROM signin_off WHERE consumer=?`, string(consumer)).Scan(&off); err != nil {
		return signin.Choice{}, fmt.Errorf("read %s sign-in choice: %w", consumer.Label(), err)
	}
	if off > 0 {
		return signin.Choice{Mode: signin.Off}, nil
	}
	profile, err := scanSigninProfile(r.read.QueryRow(`SELECT p.id,p.label,p.browser,p.profile,p.created_at FROM signin_bindings b JOIN signin_profiles p ON p.id=b.profile_id WHERE b.consumer=?`, string(consumer)))
	if errors.Is(err, sql.ErrNoRows) {
		return signin.Choice{Mode: signin.Automatic}, nil
	}
	if err != nil {
		return signin.Choice{}, fmt.Errorf("read %s sign-in profile: %w", consumer.Label(), err)
	}
	return signin.Choice{Mode: signin.Chosen, Profile: &profile}, nil
}

// BindSignin points a consumer at a profile, at the system default browser
// when ref is signin.RefAutomatic, or at nothing when ref is signin.RefOff.
func (r *Registry) BindSignin(consumer signin.Consumer, ref string) (signin.Choice, error) {
	if _, ok := signin.LookupConsumer(string(consumer)); !ok {
		return signin.Choice{}, fmt.Errorf("unknown sign-in consumer %q", consumer)
	}
	choice := signin.Choice{Mode: signin.Automatic}
	switch ref {
	case signin.RefAutomatic:
	case signin.RefOff:
		choice.Mode = signin.Off
	default:
		profile, err := r.SigninProfile(ref)
		if err != nil {
			return signin.Choice{}, err
		}
		choice = signin.Choice{Mode: signin.Chosen, Profile: &profile}
	}
	tx, err := r.db.Begin()
	if err != nil {
		return signin.Choice{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM signin_bindings WHERE consumer=?`, string(consumer)); err != nil {
		return signin.Choice{}, err
	}
	if _, err := tx.Exec(`DELETE FROM signin_off WHERE consumer=?`, string(consumer)); err != nil {
		return signin.Choice{}, err
	}
	switch choice.Mode {
	case signin.Off:
		_, err = tx.Exec(`INSERT INTO signin_off(consumer) VALUES(?)`, string(consumer))
	case signin.Chosen:
		_, err = tx.Exec(`INSERT INTO signin_bindings(consumer,profile_id) VALUES(?,?)`, string(consumer), choice.Profile.ID)
	}
	if err != nil {
		return signin.Choice{}, err
	}
	return choice, tx.Commit()
}

// SigninSelection resolves a consumer's choice against this computer's
// browsers, as ResolveSignin does for a request.
func (r *Registry) SigninSelection(consumer signin.Consumer) (signin.Selection, signin.Source, error) {
	choice, err := r.SigninChoice(consumer)
	if err != nil {
		return signin.Selection{}, signin.Source{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return signin.Selection{Mode: choice.Mode}, signin.Source{}, err
	}
	return signin.Select(home, consumer, choice)
}

func (r *Registry) ResolveSignin(target signin.Target) (signin.Source, error) {
	_, source, err := r.SigninSelection(target.Consumer)
	return source, err
}

func (r *Registry) SigninCandidates(consumer signin.Consumer) ([]signin.Source, error) {
	choice, err := r.SigninChoice(consumer)
	if err != nil {
		return nil, err
	}
	profiles, err := r.ListSigninProfiles()
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return signin.CandidatesFor(home, consumer, choice, profiles)
}
