// Package authentication assembles who may reach the API, in one place a browser
// login can replace. A person's client acts as that person, and a service client
// reaches only what its scopes list.
package authentication

import (
	"log/slog"
	"net/http"

	"github.com/datapointchris/goapiauth"
	"github.com/datapointchris/goapiauth/clientid"

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

// Protect puts a bearer token in front of routes, and limits a service client to
// what scopes lists for it.
func Protect(routes *http.ServeMux, scopes map[string][]string, verifier goapiauth.TokenVerifier, log *slog.Logger, public ...string) http.Handler {
	limited := goapiauth.LimitServices(routes, scopes, log)
	return goapiauth.RequireBearer(verifier, log, public...)(bindWriter(limited))
}

// bindWriter names the caller on the request's context, so the audit trail records
// who each write was for. A person is recorded by subject and a service by client
// id, because a service token's subject is only the client's own id or nothing.
func bindWriter(next http.Handler) http.Handler {
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
			requestlog.NoteCaller(ctx, writer.Actor, identity.ClientID, identity.Service)
		}
		next.ServeHTTP(w, r.WithContext(store.WithWriter(ctx, writer)))
	})
}
