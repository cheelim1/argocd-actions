package cmd

import (
	"errors"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/cheelim1/argocd-actions/internal/argocd"
	ctrl "github.com/cheelim1/argocd-actions/internal/controller"
)

// Sync syncs given application.
func Sync() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync ArgoCD application.",
		RunE: func(cmd *cobra.Command, args []string) error {
			address, _ := cmd.Flags().GetString("address")
			token, _ := cmd.Flags().GetString("token")
			application, _ := cmd.Flags().GetString("application")
			labels, _ := cmd.Flags().GetString("labels")
			syncRetries, _ := cmd.Flags().GetInt("sync-retries")
			syncInterval, _ := cmd.Flags().GetDuration("sync-interval")
			prune, _ := cmd.Flags().GetBool("prune")
			serverSideApply, _ := cmd.Flags().GetBool("server-side-apply")

			if syncRetries < 1 {
				return fmt.Errorf("--sync-retries must be >= 1 (got %d)", syncRetries)
			}
			if syncInterval <= 0 {
				return fmt.Errorf("--sync-interval must be > 0 (got %v)", syncInterval)
			}

			// Validation: either application or labels must be provided, but not both.
			if (application == "" && labels == "") || (application != "" && labels != "") {
				return errors.New("you must specify either 'application' or 'labels', but not both")
			}

			api := argocd.NewAPI(&argocd.APIOptions{
				Address:         address,
				Token:           token,
				SyncRetries:     syncRetries,
				SyncInterval:    syncInterval,
				ServerSideApply: serverSideApply,
			})

			controller := ctrl.NewController(api)
			log.Infof("Labels passed in: %s", labels)
			
			if application != "" {
				err := controller.Sync(application, prune, serverSideApply)
				if err != nil {
					return err
				}
				if serverSideApply {
					log.Infof("Application %s synced with server-side apply", application)
				} else {
					log.Infof("Application %s synced", application)
				}
			} else if labels != "" {
				log.Infof("Syncing apps based on labels: %s", labels)
				matchedApps, err := controller.SyncWithLabels(labels, prune, serverSideApply)
				
				if err != nil {
					log.Errorf("Error: %s", err)
					return err
				}

				for _, app := range matchedApps {
					if serverSideApply {
						log.Infof("Application %s synced using labels with server-side apply", app.Name)
					} else {
						log.Infof("Application %s synced using labels", app.Name)
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().String("application", "", "ArgoCD application name")
	cmd.Flags().String("labels", "", "Labels to sync the ArgoCD app with")
	cmd.Flags().Int("sync-retries", 5, "Number of retry attempts if sync fails")
	cmd.Flags().Duration("sync-interval", 10*time.Second, "Time to wait between retries (e.g. '5s', '1m')")
	cmd.Flags().Bool("prune", false, "Enable prune during sync (default false)")
	cmd.Flags().Bool("server-side-apply", false, "Enable server-side apply during sync (default false)")
	return cmd
}
