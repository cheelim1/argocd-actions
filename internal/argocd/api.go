package argocd

import (
	"context"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	argocdclient "github.com/argoproj/argo-cd/v2/pkg/apiclient"
	applicationpkg "github.com/argoproj/argo-cd/v2/pkg/apiclient/application"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	log "github.com/sirupsen/logrus"

	argoio "github.com/argoproj/argo-cd/v2/util/io"
)

const (
	maxRetries         = 5
	initialBackoff     = 100              // Initial backoff interval in milliseconds
	postSyncDelay      = 15 * time.Second // Delay for 15 seconds after sync
	appQueryMaxRetries = 5
	appQueryRetryDelay = 5 * time.Second
)

// Interface is an interface for API.
type Interface interface {
	Sync(appName string) error
	SyncWithLabels(labels string) ([]*v1alpha1.Application, error)
}

// API is struct for ArgoCD api.
type API struct {
	client     applicationpkg.ApplicationServiceClient
	connection io.Closer
}

// APIOptions is options for API.
type APIOptions struct {
	Address string
	Token   string
}

// NewAPI creates new API.
func NewAPI(options *APIOptions) API {
	clientOptions := argocdclient.ClientOptions{
		ServerAddr: options.Address,
		AuthToken:  options.Token,
		GRPCWeb:    true,
	}

	connection, client := argocdclient.NewClientOrDie(&clientOptions).NewApplicationClientOrDie()

	return API{client: client, connection: connection}
}

func (a API) Refresh(appName string) error {
	refreshType := "normal" // or "hard" based on your preference

	// Get the current application state with refresh using retry mechanism
	_, err := a.GetApplicationWithRetry(appName, refreshType)
	if err != nil {
		return fmt.Errorf("error refreshing application %s: %v", appName, err)
	}
	return nil
}

func (a API) GetApplicationWithRetry(appName string, refreshType string) (*v1alpha1.Application, error) {
	var appResponse *v1alpha1.Application
	var err error
	for i := 0; i < appQueryMaxRetries; i++ {
		appResponse, err = a.client.Get(context.Background(), &applicationpkg.ApplicationQuery{
			Name:    &appName,
			Refresh: &refreshType,
		})
		if err == nil {
			return appResponse, nil
		}
		log.Warnf("Error fetching application %s. Attempt %d/%d. Retrying in %v...", appName, i+1, appQueryMaxRetries, appQueryRetryDelay)
		time.Sleep(appQueryRetryDelay)
	}
	return nil, fmt.Errorf("Failed to fetch application %s after %d attempts", appName, appQueryMaxRetries)
}

// Sync syncs given application.
func (a API) Sync(appName string) error {
	maxRetries := 5
	refreshPollInterval := 5 * time.Second // Interval to check sync status after refresh
	maxRefreshPolls := 10                  // Maximum number of times to poll sync status after refresh, 12 polls at 5-second intervals = 60 seconds

	for i := 0; i < maxRetries; i++ {
		// Step 1: Refresh the application to detect latest changes
		err := a.Refresh(appName)
		if err != nil {
			return err
		}

		// Poll the sync status after refresh
		isOutOfSync := false
		for j := 0; j < maxRefreshPolls; j++ {
			hasDiff, err := a.HasDifferences(appName)
			if err != nil {
				return err
			}
			if hasDiff {
				isOutOfSync = true
				break
			}
			time.Sleep(refreshPollInterval)
		}

		// If there's no difference after polling, no need to sync
		if !isOutOfSync {
			log.Infof("No differences found for app %s after polling. Skipping sync.", appName)
			return nil
		}

		// Step 2: Sync the application
		request := applicationpkg.ApplicationSyncRequest{
			Name:  &appName,
			Prune: true,
		}

		_, err = a.client.Sync(context.Background(), &request)
		if err != nil {
			// If there's an error, retry after a short delay
			log.Warnf("Error syncing app %s. Attempt %d/%d. Retrying in 10 seconds...", appName, i+1, maxRetries)
			time.Sleep(10 * time.Second)
			continue
		}

		// If the sync was successful, break out of the loop
		log.Infof("Successfully synced app %s.", appName)
		return nil
	}

	return fmt.Errorf("failed to sync app %s after %d attempts", appName, maxRetries)
}

// SyncWithLabels syncs applications based on provided labels.
func (a API) SyncWithLabels(labels string) ([]*v1alpha1.Application, error) {
	// 1. Fetch applications based on labels
	query := &applicationpkg.ApplicationQuery{
		Selector: labels,
	}
	listResponse, err := a.client.List(context.Background(), query)

	// Retry mechanism for fetching applications based on labels
	fetchRetries := 5
	fetchRetryDelay := 10 * time.Second
	for i := 0; i < fetchRetries; i++ {
		listResponse, err = a.client.List(context.Background(), &applicationpkg.ApplicationQuery{
			Selector: labels,
		})
		if err != nil {
			argoio.Close(a.connection) // Close the connection here if there's an error
			return nil, err
		}

		if len(listResponse.Items) > 0 {
			break
		}

		// If no applications are found, retry after a delay
		if i < fetchRetries-1 { // Don't sleep after the last retry
			log.Warnf("No applications found for labels: %s. Retrying in %v...", labels, fetchRetryDelay)
			time.Sleep(fetchRetryDelay)
		}
	}

	if len(listResponse.Items) == 0 {
		log.Errorf("No applications found for labels: %s after %d retries", labels, fetchRetries)
		return nil, fmt.Errorf("No applications found for labels: %s after %d retries", labels, fetchRetries)
	}

	var syncedApps []*v1alpha1.Application
	var syncErrors []string

	// 2. Sync each application
	for _, app := range listResponse.Items {
		retries := 0
		for retries < maxRetries {
			err := a.Sync(app.Name)
			if err != nil {
				if isTransientError(err) {
					// Handle transient errors with exponential backoff
					time.Sleep(time.Duration(math.Pow(2, float64(retries))) * time.Millisecond * initialBackoff)
					retries++
					continue
				}
				syncErrors = append(syncErrors, fmt.Sprintf("Error syncing %s: %v", app.Name, err))
				break
			}

			// Introduce a post-sync delay
			time.Sleep(postSyncDelay)

			// Check for differences after sync
			hasDiff, err := a.HasDifferences(app.Name)
			if err != nil {
				syncErrors = append(syncErrors, fmt.Sprintf("Error checking differences for %s: %v", app.Name, err))
				break
			}

			if !hasDiff {
				log.Infof("Synced app %s based on labels", app.Name)
				syncedApps = append(syncedApps, &app)
				break
			} else {
				log.Warnf("Differences still found after sync for app %s. Retrying...", app.Name)
				retries++
				time.Sleep(10 * time.Second) // Wait before retrying
			}
		}
	}

	// Close the gRPC connection after all sync operations are complete
	defer argoio.Close(a.connection)

	// Return errors if any
	if len(syncErrors) > 0 {
		return syncedApps, fmt.Errorf(strings.Join(syncErrors, "; "))
	}

	return syncedApps, nil
}

// Helper function to determine if an error is transient and should be retried
func isTransientError(err error) bool {
	errMsg := err.Error()
	transientErrors := []string{
		"ENHANCE_YOUR_CALM",
		"too_many_pings",
		"error reading from server: EOF",
		"code = Unavailable desc = closing transport due to",
	}
	for _, transientError := range transientErrors {
		if strings.Contains(errMsg, transientError) {
			return true
		}
	}
	return false
}

// HasDifferences checks if the given application has differences between the desired and live state.
func (a API) HasDifferences(appName string) (bool, error) {
	defaultRefreshType := "normal" // or "hard" based on your preference

	// Get the application details with retries
	appResponse, err := a.GetApplicationWithRetry(appName, defaultRefreshType)
	if err != nil {
		return false, fmt.Errorf("Error fetching application %s: %v", appName, err)
	}

	// Check the application's sync status
	if appResponse.Status.Sync.Status == v1alpha1.SyncStatusCodeOutOfSync {
		return true, nil
	}

	return false, nil
}
