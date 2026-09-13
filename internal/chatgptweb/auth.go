package chatgptweb

import (
	"context"
	"errors"
	"time"
)

type AuthEvidence struct {
	Status             string    `json:"status"`
	ComposerReady      bool      `json:"composer_ready"`
	CheckedAt          time.Time `json:"checked_at"`
	Method             string    `json:"method"`
	GenerationVerified bool      `json:"generation_verified"`
}

// CheckAuth reads the authenticated session in a new gateway-owned tab. Tokens
// and account identity stay inside the page, and no prompt is submitted.
func (d *Driver) CheckAuth(ctx context.Context) AuthEvidence {
	evidence := AuthEvidence{Status: "unknown", CheckedAt: time.Now().UTC(), Method: "browser_session_endpoint"}
	if !d.cfg.Enabled {
		evidence.Status = "browser_disabled"
		return evidence
	}
	tab, closeTab := d.connect()
	defer closeTab()
	check, cancel := context.WithTimeout(tab, 20*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	if err := browserNavigate(check, d.origin+"/"); err != nil {
		evidence.Status = "browser_unavailable"
		return evidence
	}
	var result struct {
		Authenticated bool `json:"authenticated"`
		Ready         bool `json:"ready"`
	}
	err := evaluate(check, `if(location.origin!==args.origin)return {authenticated:false,ready:false};const a=await auth();if(args.expected_identity&&args.expected_identity!==a.user.id&&args.expected_identity!==a.user.email)throw new Error('account_changed');return {authenticated:true,ready:!!composer()};`, map[string]string{"origin": d.origin, "expected_identity": d.cfg.ExpectedIdentity}, &result)
	if err != nil {
		var classified *Error
		if errors.As(err, &classified) && classified.Code == 401 {
			evidence.Status = "login_required"
		}
		if errors.As(err, &classified) && classified.Code == 403 {
			evidence.Status = "challenge_or_access_denied"
			if classified.Message == "ChatGPT web: account_changed" {
				evidence.Status = "account_mismatch"
			}
		}
		if errors.As(err, &classified) && classified.Code == 429 {
			evidence.Status = "rate_limited"
		}
		return evidence
	}
	if result.Authenticated {
		evidence.Status = "authenticated"
		evidence.ComposerReady = result.Ready
	}
	return evidence
}
