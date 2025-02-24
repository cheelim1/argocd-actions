package controller

import (
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/cheelim1/argocd-actions/internal/argocd"
)

// Action is action type.
type Action int

// Controller is the main struct.
type Controller struct {
	API argocd.Interface
}

// NewController creates a new Controller.
func NewController(api argocd.Interface) *Controller {
	return &Controller{API: api}
}

// Sync syncs the given application with the optional prune flag.
func (c Controller) Sync(appName string, prune bool) error {
	return c.API.Sync(appName, prune)
}

// SyncWithLabels syncs applications based on provided labels with the prune option.
func (c Controller) SyncWithLabels(labels string, prune bool) ([]*v1alpha1.Application, error) {
	return c.API.SyncWithLabels(labels, prune)
}
