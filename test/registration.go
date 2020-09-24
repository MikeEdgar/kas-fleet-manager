package test

import (
	"gitlab.cee.redhat.com/service/managed-services-api/pkg/config"
	"net/http/httptest"
	"testing"

	gm "github.com/onsi/gomega"

	"gitlab.cee.redhat.com/service/managed-services-api/pkg/api/openapi"
)

// Register a test
// This should be run before every integration test
func RegisterIntegration(t *testing.T, server *httptest.Server) (*Helper, *openapi.APIClient) {
	// Register the test with gomega
	gm.RegisterTestingT(t)
	// Create a new helper
	helper := NewHelper(t, server)
	if server != nil && helper.Env().Config.OCM.MockMode == config.MockModeEmulateServer {
		helper.SetServer(server)
	}
	helper.StartServer()
	// Reset the database to a seeded blank state
	helper.ResetDB()
	// Create an api client
	client := helper.NewApiClient()
	return helper, client
}
