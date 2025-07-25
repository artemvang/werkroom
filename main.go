package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// =============================================================================
// CONFIGURATION AND CONSTANTS
// =============================================================================

var (
	// UI Constants
	DefaultWidth  = 20
	DefaultHeight = 14
	MinHeight     = 5
	UIOverhead    = 7 // title, margins, help text
)

// =============================================================================
// STYLING
// =============================================================================

type Styles struct {
	Title        lipgloss.Style
	Item         lipgloss.Style
	SelectedItem lipgloss.Style
	Pagination   lipgloss.Style
	Help         lipgloss.Style
	QuitText     lipgloss.Style
	NoItems      lipgloss.Style
	Filter       lipgloss.Style

	// Status colors
	Running      lipgloss.Style
	Terminated   lipgloss.Style
	Provisioning lipgloss.Style
	Stopping     lipgloss.Style

	// Tree styles
	Group     lipgloss.Style
	Expanded  lipgloss.Style
	Collapsed lipgloss.Style
}

func NewStyles() Styles {
	return Styles{
		Title:        lipgloss.NewStyle().MarginLeft(2),
		Item:         lipgloss.NewStyle().PaddingLeft(4),
		SelectedItem: lipgloss.NewStyle().PaddingLeft(2).Foreground(lipgloss.Color("170")),
		Pagination:   list.DefaultStyles().PaginationStyle.PaddingLeft(4),
		Help:         list.DefaultStyles().HelpStyle.PaddingLeft(4).PaddingBottom(1),
		QuitText:     lipgloss.NewStyle().Margin(1, 0, 2, 4),
		NoItems:      lipgloss.NewStyle().MarginLeft(2).PaddingLeft(4),
		Filter:       lipgloss.NewStyle().Foreground(lipgloss.Color("2")),

		Running:      lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		Terminated:   lipgloss.NewStyle().Foreground(lipgloss.Color("8")),
		Provisioning: lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		Stopping:     lipgloss.NewStyle().Foreground(lipgloss.Color("1")),

		Group:     lipgloss.NewStyle().Foreground(lipgloss.Color("4")),
		Expanded:  lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
		Collapsed: lipgloss.NewStyle().Foreground(lipgloss.Color("4")),
	}
}

// =============================================================================
// DOMAIN MODELS
// =============================================================================

// Project represents a GCP project
type Project struct {
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Status    string `json:"lifecycleState"`
}

// VM represents a GCP VM instance
type VM struct {
	Name     string    `json:"name"`
	Zone     string    `json:"zone"`
	Status   string    `json:"status"`
	Metadata *Metadata `json:"metadata,omitempty"`
}

// Metadata represents VM metadata
type Metadata struct {
	Items []MetadataItem `json:"items,omitempty"`
}

// MetadataItem represents a single metadata key-value pair
type MetadataItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// VMStatus represents possible VM states
type VMStatus string

const (
	StatusRunning      VMStatus = "RUNNING"
	StatusTerminated   VMStatus = "TERMINATED"
	StatusProvisioning VMStatus = "PROVISIONING"
	StatusStopping     VMStatus = "STOPPING"
)

// GetAbbreviation returns single-letter status abbreviation
func (s VMStatus) GetAbbreviation() string {
	switch s {
	case StatusRunning:
		return "R"
	case StatusTerminated:
		return "T"
	case StatusProvisioning:
		return "P"
	case StatusStopping:
		return "S"
	default:
		return "?"
	}
}

// GetStyle returns appropriate styling for the status
func (s VMStatus) GetStyle(styles Styles) lipgloss.Style {
	switch s {
	case StatusRunning:
		return styles.Running
	case StatusTerminated:
		return styles.Terminated
	case StatusProvisioning:
		return styles.Provisioning
	case StatusStopping:
		return styles.Stopping
	default:
		return styles.Item
	}
}

// GetInstanceGroup extracts instance group from VM metadata
func (vm VM) GetInstanceGroup() string {
	if vm.Metadata == nil || vm.Metadata.Items == nil {
		return ""
	}

	for _, item := range vm.Metadata.Items {
		if item.Key == "created-by" && strings.Contains(item.Value, "instanceGroupManagers/") {
			parts := strings.Split(item.Value, "/")
			for i, part := range parts {
				if part == "instanceGroupManagers" && i+1 < len(parts) {
					return parts[i+1]
				}
			}
		}
	}
	return ""
}

// =============================================================================
// TREE DATA STRUCTURE
// =============================================================================

// NodeType represents the type of tree node
type NodeType int

const (
	GroupNode NodeType = iota
	InstanceNode
)

// TreeNode represents a node in the tree structure
type TreeNode struct {
	Type       NodeType
	Name       string
	VM         *VM
	GroupName  string
	IsExpanded bool
	Children   []*TreeNode
	Depth      int
}

// TreeManager handles tree operations
type TreeManager struct {
	nodes  []*TreeNode
	styles Styles
}

// NewTreeManager creates a new tree manager
func NewTreeManager(styles Styles) *TreeManager {
	return &TreeManager{
		styles: styles,
	}
}

// BuildFromVMs creates tree structure from VM list
func (tm *TreeManager) BuildFromVMs(vms []VM) {
	groups := make(map[string][]*VM)
	var ungrouped []*VM

	// Group VMs by instance group
	for i := range vms {
		vm := &vms[i]
		if groupName := vm.GetInstanceGroup(); groupName != "" {
			groups[groupName] = append(groups[groupName], vm)
		} else {
			ungrouped = append(ungrouped, vm)
		}
	}

	var nodes []*TreeNode

	// Create sorted group nodes
	var groupNames []string
	for groupName := range groups {
		groupNames = append(groupNames, groupName)
	}
	sort.Strings(groupNames)

	for _, groupName := range groupNames {
		groupNode := &TreeNode{
			Type:       GroupNode,
			Name:       groupName,
			GroupName:  groupName,
			IsExpanded: false,
			Depth:      0,
			Children:   make([]*TreeNode, 0),
		}

		// Add child instances
		for _, vm := range groups[groupName] {
			instanceNode := &TreeNode{
				Type:      InstanceNode,
				Name:      vm.Name,
				VM:        vm,
				GroupName: groupName,
				Depth:     1,
			}
			groupNode.Children = append(groupNode.Children, instanceNode)
		}

		nodes = append(nodes, groupNode)
	}

	// Add ungrouped instances
	for _, vm := range ungrouped {
		instanceNode := &TreeNode{
			Type:  InstanceNode,
			Name:  vm.Name,
			VM:    vm,
			Depth: 0,
		}
		nodes = append(nodes, instanceNode)
	}

	tm.nodes = nodes
}

// GetNodes returns all tree nodes
func (tm *TreeManager) GetNodes() []*TreeNode {
	return tm.nodes
}

// FlattenForDisplay converts tree to flat list for UI
func (tm *TreeManager) FlattenForDisplay() []*TreeNode {
	var result []*TreeNode

	for _, node := range tm.nodes {
		result = append(result, node)
		if node.Type == GroupNode && node.IsExpanded {
			result = append(result, node.Children...)
		}
	}

	return result
}

// ToggleNode expands/collapses a group node
func (tm *TreeManager) ToggleNode(targetNode *TreeNode) {
	if targetNode.Type != GroupNode {
		return
	}

	for _, node := range tm.nodes {
		if node.Type == GroupNode && node.Name == targetNode.Name {
			node.IsExpanded = !node.IsExpanded
			break
		}
	}
}

// RenderNode returns formatted string for a tree node
func (tm *TreeManager) RenderNode(node *TreeNode) string {
	indent := strings.Repeat("  ", node.Depth)

	if node.Type == GroupNode {
		icon := "▶"
		style := tm.styles.Collapsed
		if node.IsExpanded {
			icon = "▼"
			style = tm.styles.Expanded
		}
		return fmt.Sprintf("%s%s %s (%d instances)",
			indent,
			style.Render(icon),
			tm.styles.Group.Render(node.Name),
			len(node.Children))
	}

	// Instance node
	status := VMStatus(node.VM.Status)
	statusStyle := status.GetStyle(tm.styles)
	coloredStatus := statusStyle.Render("[" + status.GetAbbreviation() + "]")
	return fmt.Sprintf("%s%s %s", indent, coloredStatus, node.Name)
}

// =============================================================================
// FILTERING SERVICE
// =============================================================================

// FilterService handles tree filtering
type FilterService struct {
	treeManager *TreeManager
}

// NewFilterService creates a new filter service
func NewFilterService(treeManager *TreeManager) *FilterService {
	return &FilterService{
		treeManager: treeManager,
	}
}

// Filter returns filtered tree nodes
func (fs *FilterService) Filter(nodes []*TreeNode, filterText string) []*TreeNode {
	if filterText == "" {
		return nodes
	}

	var filtered []*TreeNode
	filterLower := strings.ToLower(filterText)

	for _, node := range nodes {
		if node.Type == GroupNode {
			if strings.Contains(strings.ToLower(node.Name), filterLower) {
				// Group name matches - include entire group
				filteredGroup := &TreeNode{
					Type:       GroupNode,
					Name:       node.Name,
					GroupName:  node.GroupName,
					IsExpanded: true, // Auto-expand
					Children:   node.Children,
					Depth:      node.Depth,
				}
				filtered = append(filtered, filteredGroup)
				continue
			}

			// Check for matching children (by name only)
			var matchingChildren []*TreeNode
			for _, child := range node.Children {
				if strings.Contains(strings.ToLower(child.Name), filterLower) {
					matchingChildren = append(matchingChildren, child)
				}
			}

			if len(matchingChildren) > 0 {
				filteredGroup := &TreeNode{
					Type:       GroupNode,
					Name:       node.Name,
					GroupName:  node.GroupName,
					IsExpanded: true, // Auto-expand
					Children:   matchingChildren,
					Depth:      node.Depth,
				}
				filtered = append(filtered, filteredGroup)
			}
		} else {
			// Instance node - search name only
			if strings.Contains(strings.ToLower(node.Name), filterLower) {
				filtered = append(filtered, node)
			}
		}
	}

	return filtered
}

// =============================================================================
// GCP SERVICE
// =============================================================================

// GCPService handles GCP operations
type GCPService struct{}

// NewGCPService creates a new GCP service
func NewGCPService() *GCPService {
	return &GCPService{}
}

// LoadProjects loads available GCP projects
func (gcp *GCPService) LoadProjects() tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("gcloud", "projects", "list",
			"--format", "json(projectId,name,lifecycleState)")

		output, err := cmd.Output()
		if err != nil {
			return ErrorMsg{fmt.Errorf("failed to list projects: %w", err)}
		}

		var projects []Project
		if err := json.Unmarshal(output, &projects); err != nil {
			return ErrorMsg{fmt.Errorf("failed to parse project data: %w", err)}
		}

		// Filter only active projects
		var activeProjects []Project
		for _, project := range projects {
			if project.Status == "ACTIVE" {
				activeProjects = append(activeProjects, project)
			}
		}

		return ProjectsLoadedMsg{activeProjects}
	}
}

// LoadVMs loads VMs from GCP project
func (gcp *GCPService) LoadVMs(project string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("gcloud", "compute", "instances", "list",
			"--project", project,
			"--format", "json(name,zone,status,metadata.items)")

		output, err := cmd.Output()
		if err != nil {
			return ErrorMsg{fmt.Errorf("failed to list VMs: %w", err)}
		}

		var vms []VM
		if err := json.Unmarshal(output, &vms); err != nil {
			return ErrorMsg{fmt.Errorf("failed to parse VM data: %w", err)}
		}

		return VMsLoadedMsg{vms}
	}
}

// ConnectSSH establishes SSH connection to VM
func (gcp *GCPService) ConnectSSH(project, vmName, zone string) error {
	zoneParts := strings.Split(zone, "/")
	zoneName := zoneParts[len(zoneParts)-1]

	gcloudPath, err := exec.LookPath("gcloud")
	if err != nil {
		return fmt.Errorf("gcloud not found in PATH: %w", err)
	}

	args := []string{
		"gcloud", "compute", "ssh", vmName,
		"--project", project,
		"--zone", zoneName,
	}

	return syscall.Exec(gcloudPath, args, os.Environ())
}

// =============================================================================
// MESSAGES
// =============================================================================

// ProjectsLoadedMsg indicates projects have been loaded
type ProjectsLoadedMsg struct {
	Projects []Project
}

// VMsLoadedMsg indicates VMs have been loaded
type VMsLoadedMsg struct {
	VMs []VM
}

// ErrorMsg indicates an error occurred
type ErrorMsg struct {
	Err error
}

// =============================================================================
// APPLICATION STATE
// =============================================================================

// AppState represents the current application state
type AppState int

const (
	StateLoadingProjects AppState = iota
	StateSelectingProject
	StateLoadingVMs
	StateSelectingVM
	StateReadyToConnect
	StateQuitting
)

// =============================================================================
// BUBBLE TEA MODEL
// =============================================================================

type model struct {
	// State
	state    AppState
	quitting bool

	// Services
	gcpService    *GCPService
	treeManager   *TreeManager
	filterService *FilterService
	styles        Styles

	// Data
	projects        []Project
	selectedProject string
	selectedVM      *VM
	err             error

	// UI
	list                    list.Model
	currentlyDisplayedNodes []*TreeNode // Track what's currently shown in the list

	// Filtering
	filtering  bool
	filterText string
}

// =============================================================================
// LIST DELEGATE
// =============================================================================

type itemDelegate struct {
	styles Styles
}

type item string

func (i item) FilterValue() string { return string(i) }

func (d itemDelegate) Height() int                             { return 1 }
func (d itemDelegate) Spacing() int                            { return 0 }
func (d itemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d itemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(item)
	if !ok {
		return
	}

	str := fmt.Sprintf("%d. %s", index+1, string(i))

	fn := d.styles.Item.PaddingLeft(4).Render
	if index == m.Index() {
		fn = func(s ...string) string {
			return d.styles.SelectedItem.Render("> " + strings.Join(s, " "))
		}
	}

	fmt.Fprint(w, fn(str))
}

// =============================================================================
// MODEL IMPLEMENTATION
// =============================================================================

// newModel creates a new application model
func newModel(project string) model {
	styles := NewStyles()
	gcpService := NewGCPService()
	treeManager := NewTreeManager(styles)
	filterService := NewFilterService(treeManager)

	var items []list.Item
	var state AppState
	var title string

	if project != "" {
		// Project provided via command line - skip to loading VMs
		state = StateLoadingVMs
		title = "Loading VMs..."
		items = []list.Item{}
	} else {
		// No project provided - start by loading available projects
		state = StateLoadingProjects
		title = "Loading GCP Projects..."
		items = []list.Item{}
	}

	l := list.New(items, itemDelegate{styles: styles}, DefaultWidth, DefaultHeight)
	l.Title = title
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.Styles.Title = styles.Title
	l.Styles.PaginationStyle = styles.Pagination
	l.Styles.HelpStyle = styles.Help
	l.Styles.NoItems = styles.NoItems

	return model{
		state:           state,
		gcpService:      gcpService,
		treeManager:     treeManager,
		filterService:   filterService,
		styles:          styles,
		selectedProject: project,
		list:            l,
	}
}

// getCurrentNode returns the currently selected tree node
func (m model) getCurrentNode() *TreeNode {
	if m.list.Index() < 0 || len(m.currentlyDisplayedNodes) == 0 {
		return nil
	}

	if m.list.Index() >= len(m.currentlyDisplayedNodes) {
		return nil
	}

	return m.currentlyDisplayedNodes[m.list.Index()]
}

// updateVMList refreshes the VM list display
func (m *model) updateVMList() {
	var nodesToShow []*TreeNode
	if m.filtering && m.filterText != "" {
		nodesToShow = m.filterService.Filter(m.treeManager.GetNodes(), m.filterText)
	} else {
		nodesToShow = m.treeManager.GetNodes()
	}

	// Update tree manager nodes for display temporarily
	originalNodes := m.treeManager.GetNodes()
	m.treeManager.nodes = nodesToShow
	flatNodes := m.treeManager.FlattenForDisplay()
	m.treeManager.nodes = originalNodes // Restore original

	// Store the currently displayed nodes for getCurrentNode()
	m.currentlyDisplayedNodes = flatNodes

	// Create list items
	items := make([]list.Item, len(flatNodes))
	for i, node := range flatNodes {
		items[i] = item(m.treeManager.RenderNode(node))
	}

	m.list.SetItems(items)

	// Update title
	baseTitle := fmt.Sprintf("Sunrise Parabellum\nSelect VM from project: %s", m.selectedProject)
	if m.filtering {
		filterText := m.styles.Filter.Render("Filter:") + " " + m.filterText
		m.list.Title = fmt.Sprintf("%s\n%s", baseTitle, filterText)
	} else {
		m.list.Title = baseTitle
	}
}

// Init implements tea.Model
func (m model) Init() tea.Cmd {
	if m.selectedProject != "" && m.state == StateLoadingVMs {
		return m.gcpService.LoadVMs(m.selectedProject)
	} else if m.state == StateLoadingProjects {
		return m.gcpService.LoadProjects()
	}
	return nil
}

// Update implements tea.Model
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		availableHeight := msg.Height - UIOverhead
		if availableHeight < MinHeight {
			availableHeight = MinHeight
		}
		m.list.SetWidth(msg.Width)
		m.list.SetHeight(availableHeight)
		return m, nil

	case tea.KeyMsg:
		// Handle navigation keys first (up/down arrows) - always pass to list
		keypress := msg.String()
		if m.shouldHandleNavigation(keypress) {
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			return m, cmd
		}

		// Handle custom keys
		return m.handleKeyPress(msg)

	case ProjectsLoadedMsg:
		m.projects = msg.Projects
		m.state = StateSelectingProject

		// Create list items for projects
		items := make([]list.Item, len(m.projects))
		for i, project := range m.projects {
			items[i] = item(fmt.Sprintf("%s (%s)", project.ProjectID, project.Name))
		}

		m.list.SetItems(items)
		m.list.Title = "Select GCP Project"
		return m, nil

	case VMsLoadedMsg:
		m.state = StateSelectingVM
		m.filtering = false
		m.filterText = ""
		m.treeManager.BuildFromVMs(msg.VMs)
		m.updateVMList() // This will set currentlyDisplayedNodes
		return m, nil

	case ErrorMsg:
		m.err = msg.Err
		return m, nil
	}

	return m, nil
}

// handleKeyPress handles keyboard input
func (m model) handleKeyPress(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	keypress := msg.String()

	// Handle filtering input first
	if m.state == StateSelectingVM && m.filtering {
		return m.handleFilteringInput(keypress)
	}

	// Handle tree navigation for VM selection
	if m.state == StateSelectingVM {
		return m.handleVMSelection(keypress)
	}

	// Handle global keys
	return m.handleGlobalKeys(keypress)
}

// handleFilteringInput handles input during filtering
func (m model) handleFilteringInput(keypress string) (tea.Model, tea.Cmd) {
	switch keypress {
	case "esc":
		m.filtering = false
		m.filterText = ""
		m.updateVMList()
		return m, nil
	case "backspace", "ctrl+h":
		if len(m.filterText) > 0 {
			m.filterText = m.filterText[:len(m.filterText)-1]
			m.updateVMList()
		}
		return m, nil
	case "enter":
		currentNode := m.getCurrentNode()
		if currentNode != nil {
			if currentNode.Type == InstanceNode {
				m.selectedVM = currentNode.VM
				m.state = StateReadyToConnect
				return m, tea.Quit
			} else if currentNode.Type == GroupNode {
				// Find and toggle the original node in the tree manager
				for _, originalNode := range m.treeManager.GetNodes() {
					if originalNode.Type == GroupNode && originalNode.Name == currentNode.Name {
						originalNode.IsExpanded = !originalNode.IsExpanded
						break
					}
				}
				m.updateVMList() // Refresh the filtered view
			}
		}
		return m, nil
	default:
		if len(keypress) == 1 && isValidFilterChar(keypress[0]) {
			m.filterText += keypress
			m.updateVMList()
		}
		return m, nil
	}
}

// shouldHandleNavigation determines if key should be passed to list for navigation
func (m model) shouldHandleNavigation(keypress string) bool {
	// Only handle navigation in appropriate states
	if m.state != StateSelectingProject && m.state != StateSelectingVM {
		return false
	}

	// Check if it's a navigation key
	switch keypress {
	case "up", "k", "down", "j", "pgup", "pgdown", "home", "end":
		return true
	}
	return false
}

// handleVMSelection handles VM selection navigation
func (m model) handleVMSelection(keypress string) (tea.Model, tea.Cmd) {
	switch keypress {
	case "right":
		if currentNode := m.getCurrentNode(); currentNode != nil && currentNode.Type == GroupNode && !currentNode.IsExpanded {
			m.treeManager.ToggleNode(currentNode)
			m.updateVMList()
		}
		return m, nil
	case "left":
		if currentNode := m.getCurrentNode(); currentNode != nil && currentNode.Type == GroupNode && currentNode.IsExpanded {
			m.treeManager.ToggleNode(currentNode)
			m.updateVMList()
		}
		return m, nil
	case "space":
		if currentNode := m.getCurrentNode(); currentNode != nil && currentNode.Type == GroupNode {
			m.treeManager.ToggleNode(currentNode)
			m.updateVMList()
		}
		return m, nil
	case "enter":
		return m.handleEnterOnVM()
	case "/":
		if !m.filtering {
			m.filtering = true
			m.filterText = ""
			m.updateVMList()
		}
		return m, nil
	case "esc":
		return m.goBackToProjectSelection()
	case "q":
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

// handleEnterOnVM handles enter key on VM selection
func (m model) handleEnterOnVM() (tea.Model, tea.Cmd) {
	currentNode := m.getCurrentNode()
	if currentNode == nil {
		return m, nil
	}

	if currentNode.Type == GroupNode {
		m.treeManager.ToggleNode(currentNode)
		m.updateVMList()
	} else if currentNode.Type == InstanceNode {
		m.selectedVM = currentNode.VM
		m.state = StateReadyToConnect
		return m, tea.Quit
	}
	return m, nil
}

// handleGlobalKeys handles global keyboard shortcuts
func (m model) handleGlobalKeys(keypress string) (tea.Model, tea.Cmd) {
	switch keypress {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "q":
		if m.state == StateSelectingProject || m.state == StateLoadingProjects {
			m.quitting = true
			return m, tea.Quit
		}
	case "enter":
		if m.state == StateSelectingProject {
			if i, ok := m.list.SelectedItem().(item); ok {
				// Extract project ID from the display string "projectId (projectName)"
				projectDisplay := string(i)
				projectID := strings.Split(projectDisplay, " (")[0]
				m.selectedProject = projectID
				m.state = StateLoadingVMs
				m.list.Title = "Loading VMs..."
				return m, m.gcpService.LoadVMs(m.selectedProject)
			}
		}
	}
	return m, nil
}

// goBackToProjectSelection returns to project selection
func (m model) goBackToProjectSelection() (tea.Model, tea.Cmd) {
	items := make([]list.Item, len(m.projects))
	for i, project := range m.projects {
		items[i] = item(fmt.Sprintf("%s (%s)", project.ProjectID, project.Name))
	}
	m.list.SetItems(items)
	m.list.Title = "Select GCP Project"
	m.state = StateSelectingProject
	m.currentlyDisplayedNodes = nil // Clear displayed nodes
	return m, nil
}

// isValidFilterChar checks if character is valid for filtering
func isValidFilterChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '-' || c == '_' || c == ' '
}

// View implements tea.Model
func (m model) View() string {
	if m.quitting {
		return m.styles.QuitText.Render("Goodbye!")
	}

	if m.state == StateLoadingProjects {
		return "\n  Loading GCP projects...\n\n"
	}

	if m.state == StateLoadingVMs {
		return fmt.Sprintf("\n  Loading VMs for project: %s\n\n", m.selectedProject)
	}

	if m.state == StateReadyToConnect {
		return fmt.Sprintf("\n  Connecting to %s...\n\n", m.selectedVM.Name)
	}

	if m.err != nil {
		return fmt.Sprintf("\n  Error: %v\n\n  Press 'q' to quit.\n", m.err)
	}

	s := "\n" + m.list.View()

	if m.state == StateSelectingProject {
		s += "\n\n  Press Enter to select, 'q' to quit"
	} else if m.state == StateSelectingVM {
		if m.filtering {
			s += "\n  Press Enter to connect, Backspace to edit, Esc to clear filter, 'q' to quit"
		} else {
			s += "\n\n  Press Enter to select/expand, → to expand, ← to collapse, Space to toggle, '/' to filter, Esc to go back, 'q' to quit"
		}
	}

	return s
}

// =============================================================================
// MAIN APPLICATION
// =============================================================================

func main() {
	// Parse command line arguments
	projectFlag := flag.String("project", "", "GCP project ID to use (skips project selection)")
	flag.Parse()

	// Check dependencies
	if _, err := exec.LookPath("gcloud"); err != nil {
		log.Fatal("gcloud CLI is required but not installed. Please install Google Cloud SDK.")
	}

	// If project is provided, validate it exists (but don't exit if it doesn't - let gcloud handle the error)
	selectedProject := *projectFlag

	// Create and run application
	program := tea.NewProgram(newModel(selectedProject), tea.WithAltScreen())

	finalModel, err := program.Run()
	if err != nil {
		fmt.Printf("Error running program: %v", err)
		os.Exit(1)
	}

	// Handle SSH connection
	handleSSHConnection(finalModel)
}

// handleSSHConnection handles SSH connection after program exit
func handleSSHConnection(finalModel tea.Model) {
	if m, ok := finalModel.(model); ok && m.state == StateReadyToConnect && !m.quitting {
		fmt.Printf("Connecting to %s in project %s...\n", m.selectedVM.Name, m.selectedProject)

		if err := m.gcpService.ConnectSSH(m.selectedProject, m.selectedVM.Name, m.selectedVM.Zone); err != nil {
			fmt.Printf("SSH connection failed: %v\n", err)
			os.Exit(1)
		}
	}
}
