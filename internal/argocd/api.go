package argocd

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	argocdclient "github.com/argoproj/argo-cd/v2/pkg/apiclient"
	applicationpkg "github.com/argoproj/argo-cd/v2/pkg/apiclient/application"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	log "github.com/sirupsen/logrus"

	argoio "github.com/argoproj/argo-cd/v2/util/io"
)

const (
    maxRetries       = 5
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
    for i := 0; i < maxRetries; i++ {
        if err := a.RefreshAndSync(appName); err != nil {
            log.Warnf("Error syncing app %s. Attempt %d/%d. Retrying...", appName, i+1, maxRetries)
            // Exponential backoff with jitter
            backoff := time.Duration(initialBackoff*math.Pow(2, float64(i))) * time.Millisecond
            jitter := time.Duration(rand.Intn(100)) * time.Millisecond
            time.Sleep(backoff + jitter)
            continue
        }
        return nil
    }
    return fmt.Errorf("Failed to sync app %s after %d attempts", appName, maxRetries)
}

func (a API) RefreshAndSync(appName string) error {
    if err := a.Refresh(appName); err != nil {
        return err
    }

    hasDiff, err := a.HasDifferences(appName)
    if err != nil {
        return err
    }

    if !hasDiff {
        log.Infof("No differences found for app %s. Skipping sync.", appName)
        return nil
    }

    request := applicationpkg.ApplicationSyncRequest{
        Name:  &appName,
        Prune: false, //Disable prune
    }
    _, err = a.client.Sync(context.Background(), &request)
    return err
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
    var syncErrorsMutex sync.Mutex // Mutex to protect concurrent writes to syncErrors

    var wg sync.WaitGroup
    sem := make(chan struct{}, maxConcurrentOps) // Semaphore for concurrency control

    // 2. Sync each application in parallel
    for _, app := range listResponse.Items {
        wg.Add(1)
        go func(app v1alpha1.Application) {
            defer wg.Done()
            sem <- struct{}{} // Acquire
            defer func() { <-sem }() // Release

            if err := a.Sync(app.Name); err != nil {
                syncErrorsMutex.Lock()
                syncErrors = append(syncErrors, fmt.Sprintf("Error syncing %s: %v", app.Name, err))
                syncErrorsMutex.Unlock()
            } else {
                log.Infof("Synced app %s based on labels", app.Name)
                // Note: This operation is thread-safe for slices as long as only one goroutine writes to a particular index
                syncedApps = append(syncedApps, &app)
            }
        }(app)
    }

    wg.Wait() // Wait for all goroutines to finish

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
