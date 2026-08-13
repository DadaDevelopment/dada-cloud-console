package cliapp

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/dada-tuda/console/cli/internal/auth"
)

func httpClient() *http.Client {
	return &http.Client{}
}

// EnsureLoggedIn guarantees a usable token before a command starts talking to
// the console. When nothing is cached, or the cached session can no longer be
// refreshed, it runs the browser sign-in inline instead of failing with
// "run ddc login" - one command, not error then login then retry.
func EnsureLoggedIn(ctx context.Context, cfg Config, out io.Writer) error {
	tok, ok, err := auth.LoadToken()
	if err != nil {
		return err
	}
	if ok && !tok.Expired() {
		return nil
	}
	if ok && tok.RefreshToken != "" {
		ep := auth.EndpointsFromIssuer(cfg.Issuer)
		refreshed, refreshErr := auth.RefreshAccessToken(ep, httpClient(), cfg.ClientID, tok.RefreshToken)
		if refreshErr == nil {
			newTok := auth.FromTokenResponse(refreshed, cfg.ClientID, cfg.Issuer)
			if newTok.RefreshToken == "" {
				newTok.RefreshToken = tok.RefreshToken
			}
			if saveErr := auth.SaveToken(newTok); saveErr != nil {
				return saveErr
			}
			return nil
		}
	}

	if ok {
		fmt.Fprintln(out, "Сессия истекла - входим заново.")
	} else {
		fmt.Fprintln(out, "Вы ещё не вошли - сначала откроем вход.")
	}
	return Login(ctx, cfg, out)
}
