package store

import "context"

// OnboardingFacts is which of the features the onboarding shows have happened in an
// environment (docs/frontend.md#onboarding). Previews leave no record, so the page reports
// them itself.
type OnboardingFacts struct {
	// Network: a connection or request no rule covered was held for the user.
	Network bool
	// Push: a git push or a pull request waited for the user's review.
	Push bool
	// Browser: a teammate's browser was used, by it or by the user.
	Browser bool
	// Secret: the environment has a secret of the user's: not a provider's key, nor one of
	// Studio's own such as a forge token.
	Secret bool
}

// OnboardingFacts reports which onboarding features have happened in envID.
func (s *Store) OnboardingFacts(ctx context.Context, envID string) (OnboardingFacts, error) {
	var f OnboardingFacts
	err := s.db.QueryRowContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM approvals WHERE environment_id = ?1 AND kind IN ('network', 'network.request')),
		EXISTS (SELECT 1 FROM approvals WHERE environment_id = ?1 AND kind IN ('git.push', 'git.pr')),
		EXISTS (SELECT 1 FROM browser_actions WHERE environment_id = ?1),
		EXISTS (SELECT 1 FROM secrets WHERE environment_id = ?1 AND studio_only = 0
			AND id NOT IN (SELECT IFNULL(secret_id, '') FROM providers WHERE environment_id = ?1))`,
		envID).Scan(&f.Network, &f.Push, &f.Browser, &f.Secret)
	return f, err
}
