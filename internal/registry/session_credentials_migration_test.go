package registry

import (
	"fmt"
	"testing"
	"time"
)

func TestCredentialsLoseTheirStoredPermissionsAndStayValid(t *testing.T) {
	for _, from := range []int{44, 45} {
		t.Run(fmt.Sprintf("from v%d", from), func(t *testing.T) {
			r, session := credentialFixture(t)
			now := time.Now()
			issued, err := r.IssueSessionCredential(session, now)
			if err != nil {
				t.Fatal(err)
			}
			for _, statement := range []string{
				`ALTER TABLE session_credentials ADD COLUMN capabilities TEXT NOT NULL DEFAULT '[]'`,
				`ALTER TABLE session_credentials ADD COLUMN ceiling TEXT NOT NULL DEFAULT '{}'`,
				`ALTER TABLE session_credentials ADD COLUMN delivered INTEGER NOT NULL DEFAULT 1`,
				`UPDATE session_credentials SET capabilities='["read.self","control.project"]', delivered=0`,
				fmt.Sprintf(`PRAGMA user_version=%d`, from),
			} {
				if _, err := r.db.Exec(statement); err != nil {
					t.Fatalf("%s: %v", statement, err)
				}
			}
			path := r.diskPath()
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			tx, err := reopened.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			for _, column := range []string{"capabilities", "ceiling", "delivered"} {
				if exists, err := columnExists(tx, "session_credentials", column); err != nil || exists {
					tx.Rollback()
					t.Fatalf("%s still stored after the upgrade (err=%v)", column, err)
				}
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if credential, err := reopened.AuthenticateSessionToken(issued.Token, now); err != nil || credential.SessionID != session {
				t.Fatalf("credential after upgrade=%+v err=%v", credential, err)
			}
		})
	}
}
