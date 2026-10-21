package console

import "context"

// WithSessionSharingRevision publishes a projection only while its sharing
// policy and session are still current. WithSession holds the Store mutex for
// both this revision check and fn, ordering publication against sharing changes
// as well as logout, password changes, disablement and expiry.
//
// The caller must derive the projection from expectedRevision. A conflict means
// it must read sharing again and rebuild the projection, never reuse old data
// with a newer revision. As with WithSession, fn must not call Store methods or
// perform external network work/inference, and must honor its context deadline.
func (s *Store) WithSessionSharingRevision(ctx context.Context, token string, expectedRevision uint64, fn func(context.Context) error) error {
	return s.WithSession(ctx, token, func(bounded context.Context) error {
		if s.state.Sharing.Revision != expectedRevision {
			return ErrConflict
		}
		return fn(bounded)
	})
}
