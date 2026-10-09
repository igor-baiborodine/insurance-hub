// Package producthttp owns the direct Product HTTP routing boundary.
package producthttp

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// Callbacks let the HTTP boundary invoke the application without owning its domain types.
// Step 03 supplies test callbacks; the Product application adapter is wired in a later step.
type Callbacks struct {
	List func(http.ResponseWriter, *http.Request)
	Get  func(http.ResponseWriter, *http.Request, string)
}

// NewHandler routes direct catalog requests without normalizing encoded product codes.
func NewHandler(callbacks Callbacks) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			http.Error(writer, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		path := request.URL.EscapedPath()
		if path == "/products" || path == "/products/" {
			callbacks.List(writer, request)
			return
		}
		if !strings.HasPrefix(path, "/products/") {
			WriteNotFound(writer, request)
			return
		}

		rawCode := strings.TrimPrefix(path, "/products/")
		if rawCode == "" || strings.Contains(rawCode, "/") {
			WriteNotFound(writer, request)
			return
		}
		code, err := url.PathUnescape(rawCode)
		if err != nil {
			writeJSONError(writer, http.StatusBadRequest, "Malformed URI", "/")
			return
		}
		callbacks.Get(writer, request, code)
	})
}

// WriteNotFound preserves the incoming encoded path in the legacy self link.
func WriteNotFound(writer http.ResponseWriter, request *http.Request) {
	writeJSONError(writer, http.StatusNotFound, "Page Not Found", request.URL.EscapedPath())
}

func writeJSONError(writer http.ResponseWriter, status int, message, href string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(jsonErrorBody(message, href))
}

type errorLink struct {
	Href      string `json:"href"`
	Templated bool   `json:"templated"`
}

type errorBody struct {
	Message string `json:"message"`
	Links   struct {
		Self errorLink `json:"self"`
	} `json:"_links"`
}

func jsonErrorBody(message, href string) []byte {
	response := errorBody{Message: message}
	response.Links.Self = errorLink{Href: href}
	body, _ := json.Marshal(response)
	return body
}
