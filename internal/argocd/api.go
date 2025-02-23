package argocd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strings"
	"time"

	argocdclient "github.com/argoproj/argo-cd/v2/pkg/apiclient"
	applicationpkg "github.com/argoproj/argo-cd/v2/pkg/apiclient/application"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"

	argoio "github.com/argoproj/argo-cd/v2/util/io"
)

const (
	initialBackoff     = 100              // Initial backoff interval in ms, for transient error backoff
	postSyncDelay      = 15 * time.Second // Delay for 15 seconds after sync
	appQueryMaxRetries = 5
	appQueryRetryDelay = 5 * time.Second
)

// Interface is an interface for API.
type Interface interface {
	Sync(appName string, prune bool) error
	SyncWithLabels(labels string, prune bool) ([]*v1alpha1.Application, error)
}

// API represents the ArgoCD API.
type API struct {
	client     applicationpkg.ApplicationServiceClient
	connection io.Closer
	options    *APIOptions
}

// APIOptions holds API configuration options.
type APIOptions struct {
	Address      string
	Token        string
	SyncRetries  int
	SyncInterval time.Duration
}

// NewAPI creates a new API instance.
func NewAPI(options *APIOptions) API {
	// Default value to fallback
	if options.SyncRetries == 0 {
		options.SyncRetries = 5
	}
	if options.SyncInterval == 0 {
		options.SyncInterval = 10 * time.Second
	}

	clientOptions := argocdclient.ClientOptions{
		ServerAddr: options.Address,
		AuthToken:  options.Token,
		GRPCWeb:    true,
	}

	connection, client := argocdclient.NewClientOrDie(&clientOptions).NewApplicationClientOrDie()

	return API{client: client, connection: connection, options: options}
}

func (a API) Refresh(appName string) error {
	refreshType := "normal" // or "hard" based on your preference

	// Get the current application state with refresh using retry mechanism
	_, err := a.GetApplicationWithRetry(appName, refreshType)
	if err != nil {
		return fmt.Errorf("error refreshing application %s: %w", appName, err)
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
		slog.Warn(
			"Error fetching application",
			"appName", appName,
			"attempt", i+1,
			"maxAttempts", appQueryMaxRetries,
			"retryIn", appQueryRetryDelay,
			"error", err,
		)
		time.Sleep(appQueryRetryDelay)
	}
	return nil, fmt.Errorf("failed to fetch application %s after %d attempts", appName, appQueryMaxRetries)
}

// Sync syncs the given application with an optional prune flag.
func (a API) Sync(appName string, prune bool) error {
	maxRetries := a.options.SyncRetries
	refreshPollInterval := 5 * time.Second // Interval to check sync status after refresh
	maxRefreshPolls := 10                  // Maximum number of times to poll sync status after refresh, 12 polls at 5-second intervals = 60 seconds

	for i := 0; i < maxRetries; i++ {
		// Step 1: Refresh the application to fetch latest changes.
		err := a.Refresh(appName)
		if err != nil {
			return err
		}

		// Poll for differences.
		isOutOfSync := false
		for j := 0; j < maxRefreshPolls; j++ {
			hasDiff, diffErr := a.HasDifferences(appName)
			if diffErr != nil {
				return diffErr
			}
			if hasDiff {
				isOutOfSync = true
				break
			}
			time.Sleep(refreshPollInterval)
		}

		// If no differences, skip syncing.
		if !isOutOfSync {
			slog.Info("No differences found after polling", "appName", appName)
			return nil
		}

		// Step 2: Sync the application
		request := applicationpkg.ApplicationSyncRequest{
			Name:  &appName,
			Prune: prune,
		}

		_, err = a.client.Sync(context.Background(), &request)
		if err != nil {
			// If there's an error, retry after a short delay
			slog.Warn(
				"Error syncing app, will retry",
				"appName", appName,
				"attempt", i+1,
				"maxRetries", maxRetries,
				"retryIn", a.options.SyncInterval,
				"error", err,
			)
			time.Sleep(a.options.SyncInterval)
			continue
		}

		// If the sync was successful, break out of the loop
		slog.Info("Successfully synced app", "appName", appName)
		return nil
	}

	return fmt.Errorf("failed to sync app %s after %d attempts", appName, maxRetries)
}

// SyncWithLabels syncs applications based on provided labels with the prune option.
func (a API) SyncWithLabels(labels string, prune bool) ([]*v1alpha1.Application, error) {
	// 1. Fetch applications based on labels.
	query := &applicationpkg.ApplicationQuery{
		Selector: labels,
	}
	listResponse, err := a.client.List(context.Background(), query)
	if err != nil {
		slog.Error("Error fetching applications by labels", "labels", labels, "error", err)
	}

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
			slog.Warn("No applications found, retrying",
				"labels", labels,
				"attempt", i+1,
				"max", fetchRetries,
				"retryIn", fetchRetryDelay,
			)
			time.Sleep(fetchRetryDelay)
		}
	}

	if len(listResponse.Items) == 0 {
		slog.Error("No applications found after retries", "labels", labels, "retries", fetchRetries)
		return nil, fmt.Errorf("no applications found for labels: %s after %d retries", labels, fetchRetries)
	}

	var syncedApps []*v1alpha1.Application
	var syncErrors []string

	// 2. Sync each application
	for _, app := range listResponse.Items {
		retries := 0
		for retries < a.options.SyncRetries {
			err := a.Sync(app.Name, prune)
			if err != nil {
				if isTransientError(err) {
					// Handle transient errors with exponential backoff
					backoff := time.Duration(math.Pow(2, float64(retries))) * time.Millisecond * initialBackoff
					slog.Warn("Transient error syncing, backing off",
						"appName", app.Name,
						"attempt", retries+1,
						"backoff", backoff,
						"error", err,
					)
					time.Sleep(backoff)
					retries++
					continue
				}
				syncErrors = append(syncErrors, fmt.Sprintf("Error syncing %s: %v", app.Name, err))
				break
			}

			// Introduce a post-sync delay
			time.Sleep(postSyncDelay)

			// Check for differences after sync
			hasDiff, diffErr := a.HasDifferences(app.Name)
			if diffErr != nil {
				syncErrors = append(syncErrors, fmt.Sprintf("Error checking differences for %s: %v", app.Name, diffErr))
				break
			}

			if !hasDiff {
				slog.Info("Synced app via labels", "appName", app.Name, "labels", labels)
				syncedApps = append(syncedApps, &app)
				break
			} else {
				slog.Warn("Differences still found, retrying",
					"appName", app.Name,
					"attempt", retries+1,
					"max", a.options.SyncRetries,
					"retryIn", a.options.SyncInterval,
				)
				retries++
				time.Sleep(a.options.SyncInterval) // Wait before retrying
			}
		}
	}

	// Close the gRPC connection after all sync operations are complete
	defer argoio.Close(a.connection)

	// Return errors if any
	if len(syncErrors) > 0 {
		return syncedApps, fmt.Errorf("%s", strings.Join(syncErrors, "; "))
	}

	return syncedApps, nil
}

// isTransientError determines if an error is transient.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
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
		return false, fmt.Errorf("error fetching application %s: %v", appName, err)
	}

	// Check the application's sync status
	if appResponse.Status.Sync.Status == v1alpha1.SyncStatusCodeOutOfSync {
		return true, nil
	}

	return false, nil
}
