package argocd

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/rand"
	"strings"
	"time"

	argocdclient "github.com/argoproj/argo-cd/v2/pkg/apiclient"
	applicationpkg "github.com/argoproj/argo-cd/v2/pkg/apiclient/application"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	log "github.com/sirupsen/logrus"

	argoio "github.com/argoproj/argo-cd/v2/util/io"
)

const (
    maxRetries       = 5
    baseDelay  = 2 // Base delay in seconds for exponential backoff
    maxConcurrentOps = 10  // Maximum number of concurrent sync operations
    initialBackoff   = 100 // Initial backoff interval in milliseconds
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

    // Get the current application state with refresh
    getRequest := applicationpkg.ApplicationQuery{
        Name:    &appName,
        Refresh: &refreshType,
    }
    _, err := a.client.Get(context.Background(), &getRequest)
    return err
}

// Sync syncs given application.
func (a API) Sync(appName string) error {
    maxRetries := 5
    refreshPollInterval := 5 * time.Second  // Interval to check sync status after refresh
    maxRefreshPolls := 10  // Maximum number of times to poll sync status after refresh, 12 polls at 5-second intervals = 60 seconds

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

    return fmt.Errorf("Failed to sync app %s after %d attempts", appName, maxRetries)
}

// Introduce a retry mechanism with exponential backoff
func (a API) SyncWithRetry(appName string) error {
    var err error
    for i := 0; i < maxRetries; i++ {
        err = a.Sync(appName)
        if err == nil {
            return nil
        }
        // Exponential backoff: 2^i * 100ms. For i=0,1,2,... this results in 100ms, 200ms, 400ms, ...
        time.Sleep(time.Duration(math.Pow(2, float64(i))) * 100 * time.Millisecond)
    }
    return err
}

// SyncWithLabels syncs applications based on provided labels.
func (a API) SyncWithLabels(labels string) ([]*v1alpha1.Application, error) {
    // 1. Fetch applications based on labels
    query := &applicationpkg.ApplicationQuery{
        Selector: labels,
    }
    listResponse, err := a.client.List(context.Background(), query)
    if err != nil {
        argoio.Close(a.connection)  // Close the connection here if there's an error
        return nil, err
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
                    // Exponential backoff with jitter
                    delay := time.Second * time.Duration((baseDelay^retries) + rand.Intn(baseDelay))
                    log.Warnf("Transient error syncing app %s. Attempt %d/%d. Retrying in %v seconds...", app.Name, retries+1, maxRetries, delay.Seconds())
                    time.Sleep(delay)
                    retries++
                    continue
                }
                syncErrors = append(syncErrors, fmt.Sprintf("Error syncing %s: %v", app.Name, err))
                break
            } else {
                log.Infof("Synced app %s based on labels", app.Name)
                syncedApps = append(syncedApps, &app)
                break
            }
        }
    }

    // Close the gRPC connection after all sync operations are complete
    defer argoio.Close(a.connection)

    // Check if no applications were synced based on labels
    if len(syncedApps) == 0 {
        return nil, fmt.Errorf("No applications found with matching labels: %s", labels)
    }

    // Return errors if any
    if len(syncErrors) > 0 {
        return syncedApps, fmt.Errorf(strings.Join(syncErrors, "; "))
    }

    return syncedApps, nil
}

// Helper function to determine if an error is transient and should be retried
func isTransientError(err error) bool {
    errMsg := err.Error()
    return strings.Contains(errMsg, "ENHANCE_YOUR_CALM") || strings.Contains(errMsg, "too_many_pings")
}

func matchesLabels(app *v1alpha1.Application, labelsStr string) bool {
    pairs := strings.Split(labelsStr, ",")
    appLabels := app.ObjectMeta.Labels

    for _, pair := range pairs {
        // Handle negative matches
        if strings.Contains(pair, "!=") {
            keyValue := strings.Split(pair, "!=")
            if len(keyValue) != 2 {
                // Malformed label string
                continue
            }
            key, value := keyValue[0], keyValue[1]
            if appLabels[key] == value {
                return false
            }
        } else if strings.Contains(pair, "=") {
            keyValue := strings.Split(pair, "=")
            if len(keyValue) != 2 {
                // Malformed label string
                continue
            }
            key, value := keyValue[0], keyValue[1]
            if appLabels[key] != value {
                return false
            }
        } else if strings.Contains(pair, "notin") {
            parts := strings.Split(pair, "notin")
            if len(parts) != 2 {
                continue // or handle error
            }
            
            key := strings.TrimSpace(parts[0])
            valueStr := strings.TrimSpace(parts[1])
            
            // Trim brackets and split by comma
            values := strings.Split(strings.Trim(valueStr, "()"), ",")
            
            for _, v := range values {
                if appLabels[key] == strings.TrimSpace(v) {
                    return false
                }
            }
        } else if strings.HasPrefix(pair, "!") {
            key := strings.TrimPrefix(pair, "!")
            if _, exists := appLabels[key]; exists {
                return false
            }
        } else {
            // Existence checks
            if _, exists := appLabels[pair]; !exists {
                return false
            }
        }
    }
    return true
}

// HasDifferences checks if the given application has differences between the desired and live state.
func (a API) HasDifferences(appName string) (bool, error) {
    // Get the application details
    appResponse, err := a.client.Get(context.Background(), &applicationpkg.ApplicationQuery{
        Name: &appName,
    })
    if err != nil {
        return false, fmt.Errorf("Error fetching application %s: %v", appName, err)
    }

    // Check the application's sync status
    if appResponse.Status.Sync.Status == v1alpha1.SyncStatusCodeOutOfSync {
        return true, nil
    }

    return false, nil
}
