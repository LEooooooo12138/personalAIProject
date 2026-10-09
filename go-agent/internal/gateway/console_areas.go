package gateway

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yuanleyao/ai-agent/internal/smarthome"
)

type consoleCatalogProvider interface {
	Catalog(context.Context) (smarthome.CatalogSnapshot, error)
}

func (s *Server) consoleCatalog(ctx context.Context) (smarthome.CatalogSnapshot, error) {
	provider := s.areaCatalog
	if provider == nil && s.smartHome != nil {
		provider = s.smartHome
	}
	if provider == nil {
		code := "ha_not_configured"
		return smarthome.CatalogSnapshot{Meta: smarthome.CatalogMeta{Freshness: "unknown", Connection: "unavailable", ErrorCode: &code}}, smarthome.ErrCatalogUnavailable
	}
	return provider.Catalog(ctx)
}

func consoleCatalogError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, smarthome.ErrCatalogNotFound):
		consoleError(c, http.StatusNotFound, "not_found", "Not found")
	case errors.Is(err, smarthome.ErrCatalogInvalid):
		consoleError(c, http.StatusUnprocessableEntity, "invalid_request", "Check the supplied fields")
	default:
		consoleError(c, http.StatusServiceUnavailable, smarthome.HAErrorCode(err), "Home information is unavailable")
	}
}

func (s *Server) handleConsoleAreas(c *gin.Context) {
	snapshot, err := s.consoleCatalog(c.Request.Context())
	if err != nil {
		consoleCatalogError(c, err)
		return
	}
	result, err := snapshot.ListAreas(c.Query("q"))
	if err != nil {
		consoleCatalogError(c, err)
		return
	}
	s.consoleReadJSON(c, result)
}
func (s *Server) handleConsoleAreaDevices(c *gin.Context) {
	snapshot, err := s.consoleCatalog(c.Request.Context())
	if err != nil {
		consoleCatalogError(c, err)
		return
	}
	result, err := snapshot.ListDevices(c.Param("areaID"))
	if err != nil {
		consoleCatalogError(c, err)
		return
	}
	s.consoleReadJSON(c, result)
}
func (s *Server) handleConsoleAreaDevice(c *gin.Context) {
	snapshot, err := s.consoleCatalog(c.Request.Context())
	if err != nil {
		consoleCatalogError(c, err)
		return
	}
	result, err := snapshot.Device(c.Param("areaID"), c.Param("deviceID"))
	if err != nil {
		consoleCatalogError(c, err)
		return
	}
	s.consoleReadJSON(c, result)
}
