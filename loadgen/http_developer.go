package main

import (
	_ "embed"
	"net/http"

	swaggerui "github.com/alexliesenfeld/go-swagger-ui"
)

const developerOpenAPIPath = "/developer/openapi.json"
const developerSwaggerUIPath = "/developer/swagger/ui/"

//go:embed openapi/loadgen.openapi.json
var openAPISpec []byte

func registerDeveloperRoutes(mux *http.ServeMux) {
	mux.Handle(developerOpenAPIPath, staticDeveloperAsset("application/json", openAPISpec))
	mux.Handle(developerSwaggerUIPath, swaggerui.NewHandler(
		swaggerui.WithBasePath(developerSwaggerUIPath),
		swaggerui.WithSpecURL(developerOpenAPIPath),
		swaggerui.WithHTMLTitle("Loadgen OpenAPI"),
		swaggerui.WithTryItOutEnabled(true),
		swaggerui.WithValidatorURL(true, "none"),
	))
}

func staticDeveloperAsset(contentType string, body []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(body)
	})
}
