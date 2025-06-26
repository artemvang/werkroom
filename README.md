# Werkroom

A TUI for selecting and connecting to Google Cloud Platform VMs via SSH.

## Quick Demo

```bash
# Interactive mode - browse all projects and VMs
./werkroom

# Direct mode - skip to specific project
./werkroom -project=my-production-project
```

## Prerequisites

### Required
- **Go 1.19+** - [Download Go](https://golang.org/dl/)
- **Google Cloud SDK** - [Install gcloud](https://cloud.google.com/sdk/docs/install)
- **Authentication** - `gcloud auth login` completed

### Permissions Required
Your GCP account needs:
- `compute.instances.list` - To view VM instances
- `compute.instances.get` - To access VM details  
- `resourcemanager.projects.list` - To view available projects

## Installation

### Option 1: Install from Source
```bash
git clone https://github.com/artemvang/werkroom.git
cd werkroom
CGO_ENABLED=0 go build -o werkroom
./werkroom
```

### Option 2: Direct Build
```bash
go install https://github.com/artemvang/werkroom.git
```

### Option 3: Download Binary
Download the latest release from [GitHub Releases](https://github.com/artemvang/werkroom/releases)