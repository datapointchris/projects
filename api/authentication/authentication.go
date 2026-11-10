// Package authentication assembles who may reach the API: a bearer token, or a
// session the API's own browser login started. A person's client and a session
// act as that person, and a service client reaches only what its scopes list.
package authentication

import (
	"log/slog"
	"net/http"

	"github.com/datapointchris/goapiauth"
	"github.com/datapointchris/goapiauth/clientid"
	"github.com/datapointchris/goapiauth/tokenorsession"

	"github.com/datapointchris/projects/api/requestlog"
	"github.com/datapointchris/projects/store"
)

// applicationName prefixes the client ids this API accepts.
const applicationName = "projects"

// Config is the verifier's configuration for an issuer.
func Config(issuer string) goapiauth.Config {
	return goapiauth.Config{
		Issuer:        issuer,
		PersonPrefix:  clientid.PersonPrefix(applicationName),
		ServicePrefix: clientid.ServicePrefix(applicationName),
	}
}

// Metadata is the RFC 9728 document naming the issuer, which a client reads before
// it holds a token to learn where to log in.
func Metadata(mux *http.ServeMux, publicURL, issuer string) {
	mux.Handle("GET "+goapiauth.ProtectedResourcePath, goapiauth.ProtectedResourceMetadata{Resource: publicURL, Issuer: issuer})
}

// Protect puts a bearer token, or a browser session where sessions is not nil, in
// front of routes, and limits a service client to what scopes lists for it. A
// session acts as its person and reaches every route, as a person's token does.
// webClientID names the client a session logged in through, in each request's log.
func Protect(routes *http.ServeMux, scopes map[string][]string, verifier goapiauth.TokenVerifier, sessions tokenorsession.Sessions, webClientID string, log *slog.Logger, public ...string) http.Handler {
	limited := bindWriter(goapiauth.LimitServices(routes, scopes, log), webClientID)
	if sessions == nil {
		return goapiauth.RequireBearer(verifier, log, public...)(limited)
	}
	return tokenorsession.Require(verifier, sessions, log, public...)(limited)
}

// bindWriter names the caller on the request's context, so the audit trail records
// who each write was for, and an undo reaches only its own client's writes. A
// person is recorded by subject and a service by client id, because a service
// token's subject is only the client's own id or nothing. A session carries no
// client id, so its writer's client is webClientID.
func bindWriter(next http.Handler, webClientID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		writer := store.Writer{
			RequestID:   requestlog.RequestID(ctx),
			Traceparent: r.Header.Get(requestlog.HeaderTraceparent),
			Baggage:     r.Header.Get("baggage"),
		}
		if identity, ok := goapiauth.IdentityFrom(ctx); ok {
			writer.Actor = identity.Subject
			if identity.Service {
				writer.Actor = identity.ClientID
			}
			writer.Client = identity.ClientID
			if writer.Client == "" {
				writer.Client = webClientID
			}
			requestlog.NoteCaller(ctx, writer.Actor, writer.Client, identity.Service)
		}
		next.ServeHTTP(w, r.WithContext(store.WithWriter(ctx, writer)))
	})
}
