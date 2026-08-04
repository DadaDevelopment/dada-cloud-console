package agentchat

import (
	"context"

	"github.com/google/uuid"
)

type sessionIDCtxKey struct{}

// WithSessionID carries the conversation this turn belongs to down the request
// context, the same way WithTraceID carries the turn itself. Message
// persistence happens in a dozen places (the user message, every tool result,
// the assistant answer, the resumed half of a confirmed write) and all of them
// have to land in the same session; threading a parameter through all of them
// would mean any new call site silently writing a session-less row.
func WithSessionID(ctx context.Context, id uuid.UUID) context.Context {
	if id == uuid.Nil {
		return ctx
	}
	return context.WithValue(ctx, sessionIDCtxKey{}, id)
}

// SessionIDFrom returns the session carried by ctx, or uuid.Nil.
func SessionIDFrom(ctx context.Context) uuid.UUID {
	if ctx == nil {
		return uuid.Nil
	}
	if id, ok := ctx.Value(sessionIDCtxKey{}).(uuid.UUID); ok {
		return id
	}
	return uuid.Nil
}
