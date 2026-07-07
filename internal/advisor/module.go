package advisor

import httpserver "github.com/npmulder/ledgerly/internal/platform/http"

// Module is the advisor HTTP wiring surface used by the app builder.
type Module struct {
	service *Service
}

// NewHTTPModule wraps an already-wired advisor service for HTTP routing.
func NewHTTPModule(service *Service) *Module {
	return &Module{service: service}
}

// HTTPModule returns the platform route mount for advisor endpoints.
func (m *Module) HTTPModule() httpserver.Module {
	return httpserver.Module{
		Name:           ModuleName,
		RegisterRoutes: m.RegisterRoutes,
	}
}

// OpenAPIFragment returns the module's OpenAPI contribution.
func (m *Module) OpenAPIFragment() httpserver.OpenAPIFragment {
	return OpenAPIFragment()
}
