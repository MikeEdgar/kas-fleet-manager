package routes

import (
	"fmt"
	"github.com/bf2fc6cc711aee1a0c2a/kas-fleet-manager/data/generated/openapi"
	"github.com/bf2fc6cc711aee1a0c2a/kas-fleet-manager/internal/common"
	"github.com/bf2fc6cc711aee1a0c2a/kas-fleet-manager/internal/kafka/routes"
	"github.com/bf2fc6cc711aee1a0c2a/kas-fleet-manager/pkg/acl"
	"github.com/bf2fc6cc711aee1a0c2a/kas-fleet-manager/pkg/api"
	"github.com/bf2fc6cc711aee1a0c2a/kas-fleet-manager/pkg/auth"
	"github.com/bf2fc6cc711aee1a0c2a/kas-fleet-manager/pkg/clusters/ocm"
	"github.com/bf2fc6cc711aee1a0c2a/kas-fleet-manager/pkg/config"
	"github.com/bf2fc6cc711aee1a0c2a/kas-fleet-manager/pkg/db"
	"github.com/bf2fc6cc711aee1a0c2a/kas-fleet-manager/pkg/errors"
	coreHandlers "github.com/bf2fc6cc711aee1a0c2a/kas-fleet-manager/pkg/handlers"
	"github.com/bf2fc6cc711aee1a0c2a/kas-fleet-manager/pkg/services"
	"github.com/bf2fc6cc711aee1a0c2a/kas-fleet-manager/pkg/shared"
	"github.com/goava/di"
	handlers2 "github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	errors2 "github.com/pkg/errors"
	"net/http"
)

type options struct {
	di.Inject
	ServerConfig *config.ServerConfig
	OCMConfig    *config.OCMConfig

	OCM                   ocm.Client
	Kafka                 services.KafkaService
	CloudProviders        services.CloudProvidersService
	ConfigService         services.ConfigService
	Observatorium         services.ObservatoriumService
	Keycloak              services.KafkaKeycloakService
	DataPlaneCluster      services.DataPlaneClusterService
	DataPlaneKafkaService services.DataPlaneKafkaService
}

func NewRouteLoader(s options) common.RouteLoader {
	return &s
}

func (s *options) AddRoutes(mainRouter *mux.Router) error {
	basePath := fmt.Sprintf("%s/%s", routes.ApiEndpoint, routes.KafkasFleetManagementApiPrefix)
	err := s.buildApiBaseRouter(mainRouter, basePath, "kas-fleet-manager.yaml")
	if err != nil {
		return err
	}

	basePath = fmt.Sprintf("%s/%s", routes.ApiEndpoint, routes.OldManagedServicesApiPrefix)
	err = s.buildApiBaseRouter(mainRouter, basePath, "managed-services-api-deprecated.yaml")
	if err != nil {
		return err
	}

	return nil
}

func (s *options) buildApiBaseRouter(mainRouter *mux.Router, basePath string, openApiFilePath string) error {
	openAPIDefinitions, err := shared.LoadOpenAPISpec(openapi.Asset, openApiFilePath)
	if err != nil {
		return errors2.Wrapf(err, "can't load OpenAPI specification")
	}

	kafkaHandler := coreHandlers.NewKafkaHandler(s.Kafka, s.ConfigService)
	cloudProvidersHandler := coreHandlers.NewCloudProviderHandler(s.CloudProviders, s.ConfigService)
	errorsHandler := coreHandlers.NewErrorsHandler()
	serviceAccountsHandler := coreHandlers.NewServiceAccountHandler(s.Keycloak)
	metricsHandler := coreHandlers.NewMetricsHandler(s.Observatorium)
	serviceStatusHandler := coreHandlers.NewServiceStatusHandler(s.Kafka, s.ConfigService)

	authorizeMiddleware := acl.NewAccessControlListMiddleware(s.ConfigService).Authorize
	requireIssuer := auth.NewRequireIssuerMiddleware().RequireIssuer(s.OCMConfig.TokenIssuerURL, errors.ErrorUnauthenticated)
	requireTermsAcceptance := auth.NewRequireTermsAcceptanceMiddleware().RequireTermsAcceptance(s.ServerConfig.EnableTermsAcceptance, s.OCM, errors.ErrorTermsNotAccepted)

	// base path. Could be /api/kafkas_mgmt or /api/managed-services-api
	apiRouter := mainRouter.PathPrefix(basePath).Subrouter()

	// /v1
	apiV1Router := apiRouter.PathPrefix("/v1").Subrouter()

	//  /openapi
	apiV1Router.HandleFunc("/openapi", coreHandlers.NewOpenAPIHandler(openAPIDefinitions).Get).Methods(http.MethodGet)

	//  /errors
	apiV1ErrorsRouter := apiV1Router.PathPrefix("/errors").Subrouter()
	apiV1ErrorsRouter.HandleFunc("", errorsHandler.List).Methods(http.MethodGet)
	apiV1ErrorsRouter.HandleFunc("/{id}", errorsHandler.Get).Methods(http.MethodGet)

	// /status
	apiV1Status := apiV1Router.PathPrefix("/status").Subrouter()
	apiV1Status.HandleFunc("", serviceStatusHandler.Get).Methods(http.MethodGet)
	apiV1Status.Use(requireIssuer)

	v1Collections := []api.CollectionMetadata{}

	//  /kafkas
	v1Collections = append(v1Collections, api.CollectionMetadata{
		ID:   "kafkas",
		Kind: "KafkaList",
	})
	apiV1KafkasRouter := apiV1Router.PathPrefix("/kafkas").Subrouter()
	apiV1KafkasRouter.HandleFunc("/{id}", kafkaHandler.Get).Methods(http.MethodGet)
	apiV1KafkasRouter.HandleFunc("/{id}", kafkaHandler.Delete).Methods(http.MethodDelete)
	apiV1KafkasRouter.HandleFunc("", kafkaHandler.List).Methods(http.MethodGet)
	apiV1KafkasRouter.Use(requireIssuer)
	apiV1KafkasRouter.Use(authorizeMiddleware)

	apiV1KafkasCreateRouter := apiV1KafkasRouter.NewRoute().Subrouter()
	apiV1KafkasCreateRouter.HandleFunc("", kafkaHandler.Create).Methods(http.MethodPost)
	apiV1KafkasCreateRouter.Use(requireTermsAcceptance)

	//  /service_accounts
	v1Collections = append(v1Collections, api.CollectionMetadata{
		ID:   "service_accounts",
		Kind: "ServiceAccountList",
	})
	apiV1ServiceAccountsRouter := apiV1Router.PathPrefix("/{_:service[_]?accounts}").Subrouter()
	apiV1ServiceAccountsRouter.HandleFunc("", serviceAccountsHandler.ListServiceAccounts).Methods(http.MethodGet)
	apiV1ServiceAccountsRouter.HandleFunc("", serviceAccountsHandler.CreateServiceAccount).Methods(http.MethodPost)
	apiV1ServiceAccountsRouter.HandleFunc("/{id}", serviceAccountsHandler.DeleteServiceAccount).Methods(http.MethodDelete)
	apiV1ServiceAccountsRouter.HandleFunc("/{id}/{_:reset[-_]credentials}", serviceAccountsHandler.ResetServiceAccountCredential).Methods(http.MethodPost)
	apiV1ServiceAccountsRouter.HandleFunc("/{id}", serviceAccountsHandler.GetServiceAccountById).Methods(http.MethodGet)
	apiV1ServiceAccountsRouter.Use(requireIssuer)
	apiV1ServiceAccountsRouter.Use(authorizeMiddleware)

	//  /cloud_providers
	v1Collections = append(v1Collections, api.CollectionMetadata{
		ID:   "cloud_providers",
		Kind: "CloudProviderList",
	})
	apiV1CloudProvidersRouter := apiV1Router.PathPrefix("/cloud_providers").Subrouter()
	apiV1CloudProvidersRouter.HandleFunc("", cloudProvidersHandler.ListCloudProviders).Methods(http.MethodGet)
	apiV1CloudProvidersRouter.HandleFunc("/{id}/regions", cloudProvidersHandler.ListCloudProviderRegions).Methods(http.MethodGet)

	//  /kafkas/{id}/metrics
	apiV1MetricsRouter := apiV1KafkasRouter.PathPrefix("/{id}/metrics").Subrouter()
	apiV1MetricsRouter.HandleFunc("/query_range", metricsHandler.GetMetricsByRangeQuery).Methods(http.MethodGet)
	apiV1MetricsRouter.HandleFunc("/query", metricsHandler.GetMetricsByInstantQuery).Methods(http.MethodGet)

	v1Metadata := api.VersionMetadata{
		ID:          "v1",
		Collections: v1Collections,
	}
	apiMetadata := api.Metadata{
		ID: "kafkas_mgmt",
		Versions: []api.VersionMetadata{
			v1Metadata,
		},
	}
	apiRouter.HandleFunc("", apiMetadata.ServeHTTP).Methods(http.MethodGet)
	apiRouter.Use(coreHandlers.MetricsMiddleware)
	apiRouter.Use(db.TransactionMiddleware)
	apiRouter.Use(handlers2.CompressHandler)

	apiV1Router.HandleFunc("", v1Metadata.ServeHTTP).Methods(http.MethodGet)

	// /agent_clusters/{id}
	dataPlaneClusterHandler := coreHandlers.NewDataPlaneClusterHandler(s.DataPlaneCluster, s.ConfigService)
	dataPlaneKafkaHandler := coreHandlers.NewDataPlaneKafkaHandler(s.DataPlaneKafkaService, s.ConfigService, s.Kafka)
	apiV1DataPlaneRequestsRouter := apiV1Router.PathPrefix("/{_:agent[-_]clusters}").Subrouter()
	apiV1DataPlaneRequestsRouter.HandleFunc("/{id}", dataPlaneClusterHandler.GetDataPlaneClusterConfig).Methods(http.MethodGet)
	apiV1DataPlaneRequestsRouter.HandleFunc("/{id}/status", dataPlaneClusterHandler.UpdateDataPlaneClusterStatus).Methods(http.MethodPut)
	apiV1DataPlaneRequestsRouter.HandleFunc("/{id}/kafkas/status", dataPlaneKafkaHandler.UpdateKafkaStatuses).Methods(http.MethodPut)
	apiV1DataPlaneRequestsRouter.HandleFunc("/{id}/kafkas", dataPlaneKafkaHandler.GetAll).Methods(http.MethodGet)
	// deliberately returns 404 here if the request doesn't have the required role, so that it will appear as if the endpoint doesn't exist
	auth.UseOperatorAuthorisationMiddleware(apiV1DataPlaneRequestsRouter, auth.Kas, s.Keycloak.GetConfig().KafkaRealm.ValidIssuerURI, "id")

	return nil
}