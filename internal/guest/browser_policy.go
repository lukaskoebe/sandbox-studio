//go:build linux

package guest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// browserPolicyFile holds Chromium's managed policies. Debian's chromium reads every JSON
// file in this directory at launch, and the user can't change what it sets.
const browserPolicyFile = "/etc/chromium/policies/managed/sandbox-studio.json"

// browserPolicies keep Chromium from calling Google's services on its own. agent-browser's
// --disable-background-networking leaves these on, and each would ask the user to approve a
// host the page never needed. The comments name the host each one keeps quiet.
var browserPolicies = map[string]any{
	"BrowserSignin":                           0,             // accounts.google.com/ListAccounts
	"SyncDisabled":                            true,          // android.clients.google.com/checkin
	"ComponentUpdatesEnabled":                 false,         // update.googleapis.com
	"BrowserNetworkTimeQueriesEnabled":        false,         // clients2.google.com/time
	"AutofillAddressEnabled":                  false,         // content-autofill.googleapis.com
	"AutofillCreditCardEnabled":               false,         // content-autofill.googleapis.com
	"PasswordManagerEnabled":                  false,         // Studio fills credentials; the profile keeps none
	"GenAILocalFoundationalModelSettings":     1,             // optimizationguide-pa.googleapis.com model downloads
	"NewTabPageLocation":                      "about:blank", // start.duckduckgo.com, Debian's new tab page
	"HomepageLocation":                        "about:blank",
	"HomepageIsNewTabPage":                    false,
	"MetricsReportingEnabled":                 false,
	"UrlKeyedAnonymizedDataCollectionEnabled": false,
	"SearchSuggestEnabled":                    false,
	"NetworkPredictionOptions":                2, // no preconnects to links the page didn't open
	"TranslateEnabled":                        false,
	"SpellcheckEnabled":                       false, // dictionary downloads
	"DomainReliabilityAllowed":                false,
	"AlternateErrorPagesEnabled":              false,
	"EnableMediaRouter":                       false,
	"PromotionalTabsEnabled":                  false,
	"ShoppingListEnabled":                     false,
	"BackgroundModeEnabled":                   false,
}

// writeBrowserPolicies writes the policies to path, unless it already holds them. Chromium
// reads them when it starts, so they apply to every session launched after.
func writeBrowserPolicies(path string) error {
	want, err := json.MarshalIndent(browserPolicies, "", "  ")
	if err != nil {
		return err
	}
	if have, err := os.ReadFile(path); err == nil && bytes.Equal(have, want) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("browser policies: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, want, 0o644); err != nil {
		return fmt.Errorf("browser policies: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("browser policies: %w", err)
	}
	return nil
}
