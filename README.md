# ArgoCD Application Actions

[![GitHub Marketplace](https://img.shields.io/badge/Marketplace-Find%20and%20Replace-blue.svg?colorA=24292e&colorB=0366d6&style=flat&longCache=true&logo=data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAA4AAAAOCAYAAAAfSC3RAAAABHNCSVQICAgIfAhkiAAAAAlwSFlzAAAM6wAADOsB5dZE0gAAABl0RVh0U29mdHdhcmUAd3d3Lmlua3NjYXBlLm9yZ5vuPBoAAAERSURBVCiRhZG/SsMxFEZPfsVJ61jbxaF0cRQRcRJ9hlYn30IHN/+9iquDCOIsblIrOjqKgy5aKoJQj4O3EEtbPwhJbr6Te28CmdSKeqzeqr0YbfVIrTBKakvtOl5dtTkK+v4HfA9PEyBFCY9AGVgCBLaBp1jPAyfAJ/AAdIEG0dNAiyP7+K1qIfMdonZic6+WJoBJvQlvuwDqcXadUuqPA1NKAlexbRTAIMvMOCjTbMwl1LtI/6KWJ5Q6rT6Ht1MA58AX8Apcqqt5r2qhrgAXQC3CZ6i1+KMd9TRu3MvA3aH/fFPnBodb6oe6HM8+lYHrGdRXW8M9bMZtPXUji69lmf5Cmamq7quNLFZXD9Rq7v0Bpc1o/tp0fisAAAAASUVORK5CYII=)](https://github.com/cheelim1/argocd-actions)
[![Actions Status](https://github.com/cheelim1/argocd-actions/workflows/Build/badge.svg)](https://github.com/cheelim1/argocd-actions/actions)
[![Test Status](https://github.com/cheelim1/argocd-actions/actions/workflows/code-check.yml/badge.svg)](https://github.com/cheelim1/argocd-actions/actions)



This action will sync ArgoCD application.

## Usage

### Example workflow

This example replaces syncs ArgoCD application.

```yaml
name: My Workflow
on: [ push, pull_request ]
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - name: Sync ArgoCD Application
        uses: cheelim1/argocd-actions@v0.1.1
        with:
          address: "argocd.example.com"
          token: ${{ secrets.ARGOCD_TOKEN }}
          action: sync
          application: "my-example-app"
```

## Inputs

| Input     | Description                            |
|-----------|----------------------------------------|
| `address` | ArgoCD server address.                 |
| `token`   | ArgoCD Token.                          |
| `action`  | ArgoCD Action i.e. sync.               |
| `application` | Application name to execute action on. [Optional] |
| `labels` | ArgoCD app to sync based on labels. [Optional] |
| `sync_retries` | ArgoCD app to sync retries (Default 5). [Optional] |
| `sync_interval` | ArgoCD app to sync retry interval (Default 10s). [Optional] |

### Note
Have to either pass in application OR labels. Either 1 is required.

## Examples

### Sync Application

You can sync ArgoCD application after building an image etc.

```yaml
name: My Workflow
on: [ push, pull_request ]
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - name: Sync ArgoCD Application
        uses: cheelim1/argocd-actions@v0.1.1
        with:
          address: "argocd.example.com"
          token: ${{ secrets.ARGOCD_TOKEN }}
          action: sync
          application: "my-example-app"
          sync_retries: "3" # Optional if you want to tweak the number of retries
          sync_interval: "5s" # Optional if you want to tweak the retry sync interval
```

### Example syncing with labels
```yaml
name: My Workflow
on: [ push, pull_request ]
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - name: Sync ArgoCD Application
        uses: cheelim1/argocd-actions@v0.1.1
        with:
          address: "argocd.example.com"
          token: ${{ secrets.ARGOCD_TOKEN }}
          action: sync
          labels: "env=production,team=myteam" # Replace with your ArgoCD App label key-value pairs.
```

## Publishing 
Create a new release & bump up the version tag 🚀.
