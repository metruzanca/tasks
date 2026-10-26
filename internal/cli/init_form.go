package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/metruzanca/tasks/internal/config"
	"github.com/metruzanca/tasks/internal/native"
	"github.com/metruzanca/tasks/plugins/linear"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type initValues struct {
	Backend       string
	Write         string
	DB            string
	DBSet         bool
	Owner         string
	Project       int
	Repo          string
	StatusField   string
	PriorityField string
	Team          string
	ReadyStatus   string
	OAuthClientID string
	TokenEnv      string
}

var errInitCancelled = errors.New("init cancelled")

func hasInitFlags(cmd *cobra.Command, backend string) bool {
	if backend != "" {
		return true
	}
	for _, name := range []string{
		"write", "db", "owner", "project", "repo", "status-field",
		"priority-field", "team", "ready-status", "oauth-client-id", "token-env",
	} {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func (a *App) runInitForm(cmd *cobra.Command, s *config.Settings, v *initValues) error {
	ctx := cmd.Context()
	fillGitHubDefaults(ctx, s, v)
	if v.Backend == "linear" {
		if v.Team == "" {
			v.Team = s.Config.Linear.Team
		}
		if v.ReadyStatus == "" {
			v.ReadyStatus = s.Config.Linear.ReadyStatus
		}
		if v.OAuthClientID == "" {
			v.OAuthClientID = s.Config.Linear.OAuthClientID
		}
	}

	names, labels := a.backendChoices(v.Backend)
	backendOptions := make([]huh.Option[string], len(names))
	for i := range names {
		backendOptions[i] = huh.NewOption(labels[i], names[i])
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	writeOptions := []huh.Option[string]{
		huh.NewOption("repo    "+filepath.Join(cwd, ".tasks.toml"), "repo"),
		huh.NewOption("global  "+s.GlobalPath, "global"),
	}

	githubFields := []huh.Field{
		huh.NewInput().
			Title("GitHub owner").
			Description("The user or organization that owns the project.").
			Value(&v.Owner).
			Validate(huh.ValidateNotEmpty()),
		huh.NewInput().
			Title("GitHub repository").
			Description("Where issues live. Use owner/name, or just the repo name.").
			Value(&v.Repo).
			Validate(huh.ValidateNotEmpty()),
		huh.NewInput().
			Title("Status field").
			Description("Single-select field used as the board column.").
			Value(&v.StatusField).
			Validate(huh.ValidateNotEmpty()),
		huh.NewInput().
			Title("Priority field").
			Description("Single-select field used for priority.").
			Value(&v.PriorityField).
			Validate(huh.ValidateNotEmpty()),
	}

	linearFields := []huh.Field{}
	teamOptions := a.linearTeamOptions(ctx, v)
	if len(teamOptions) > 0 {
		linearFields = append(linearFields, huh.NewSelect[string]().
			Title("Linear team").
			Description("Teams loaded from your Linear account.").
			Options(teamOptions...).
			Value(&v.Team))
	} else {
		linearFields = append(linearFields, huh.NewInput().
			Title("Linear team").
			Description("Team key from the issue prefix, such as ENG in ENG-123.").
			Value(&v.Team).
			Validate(huh.ValidateNotEmpty()))
	}
	linearFields = append(linearFields, huh.NewInput().
		Title("Ready column").
		Description("Leave blank to use the first unstarted workflow state.").
		Value(&v.ReadyStatus))

	sqliteFields := []huh.Field{
		huh.NewInput().
			Title("SQLite database path").
			Description("Leave blank for the default: " + s.Config.SQLite.Path).
			Value(&v.DB),
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Backend").
				Options(backendOptions...).
				Value(&v.Backend),
			huh.NewSelect[string]().
				Title("Write configuration").
				Description("A repo config applies to this project only.").
				Options(writeOptions...).
				Value(&v.Write),
		).Title("Board"),
		huh.NewGroup(githubFields...).
			Title("GitHub").
			WithHideFunc(func() bool { return v.Backend != "github" }),
		huh.NewGroup(linearFields...).
			Title("Linear").
			WithHideFunc(func() bool { return v.Backend != "linear" }),
		huh.NewGroup(sqliteFields...).
			Title("SQLite").
			WithHideFunc(func() bool { return v.Backend != "sqlite" }),
	)
	if err := runForm(cmd, form); err != nil {
		return err
	}

	v.Repo = normalizeRepo(v.Owner, v.Repo)
	v.DB = strings.TrimSpace(v.DB)
	v.DBSet = v.DB != ""

	if v.Backend == "github" {
		if err := a.runGitHubProjectForm(cmd, s, v); err != nil {
			return err
		}
		if v.Project > 0 {
			if err := ensureGitHubProjectLinked(cmd.Context(), v.Owner, v.Project, v.Repo, githubToken(cmd.Context(), s)); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: project #%d is not linked to %s: %v\n", v.Project, v.Repo, err)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Linked project #%d to %s\n", v.Project, v.Repo)
			}
		}
	}
	return nil
}

func runForm(cmd *cobra.Command, form *huh.Form) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("the init form requires a terminal; use flags for non-interactive setup")
	}
	form = form.
		WithTheme(huh.ThemeCharm()).
		WithInput(cmd.InOrStdin()).
		WithOutput(cmd.OutOrStdout())
	if err := form.RunWithContext(cmd.Context()); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return errInitCancelled
		}
		return err
	}
	return nil
}

func (a *App) runGitHubProjectForm(cmd *cobra.Command, s *config.Settings, v *initValues) error {
	if err := ensureGitHubSession(cmd, s); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
	}
	token := githubToken(cmd.Context(), s)
	projects, err := listGitHubProjects(cmd.Context(), v.Owner, token)
	if err == nil {
		sort.Slice(projects, func(i, j int) bool { return projects[i].Title < projects[j].Title })
		options := make([]huh.Option[int], 0, len(projects)+2)
		found := false
		for _, p := range projects {
			options = append(options, huh.NewOption(fmt.Sprintf("%s (#%d)", p.Title, p.Number), p.Number))
			if p.Number == v.Project {
				found = true
			}
		}
		if v.Project > 0 && !found {
			options = append(options, huh.NewOption(fmt.Sprintf("Current project (#%d)", v.Project), v.Project))
		}
		options = append(options,
			huh.NewOption("Create a new GitHub project", -1),
			huh.NewOption("Enter a project number manually", 0),
		)
		switch {
		case len(projects) == 0 && v.Project == 0:
			v.Project = -1
		case v.Project == 0:
			v.Project = projects[0].Number
		}
		if err := runForm(cmd, huh.NewForm(huh.NewGroup(
			huh.NewSelect[int]().
				Title("GitHub project").
				Description("Choose an existing board, create one, or enter a number.").
				Options(options...).
				Value(&v.Project),
		))); err != nil {
			return err
		}
		if v.Project == -1 {
			return a.createGitHubProjectForm(cmd, v, token)
		}
		if v.Project > 0 {
			return nil
		}
	}

	project := ""
	if v.Project > 0 {
		project = strconv.Itoa(v.Project)
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("GitHub project number").
			Description("Open the project and copy the number from its URL: https://github.com/" + v.Owner + "/projects/<number>").
			Value(&project).
			Validate(func(s string) error {
				n, err := strconv.Atoi(strings.TrimSpace(s))
				if err != nil || n <= 0 {
					return errors.New("enter a positive project number")
				}
				return nil
			}),
	))
	if err := runForm(cmd, form); err != nil {
		return err
	}
	n, _ := strconv.Atoi(strings.TrimSpace(project))
	v.Project = n
	return nil
}

func (a *App) createGitHubProjectForm(cmd *cobra.Command, v *initValues, token string) error {
	title := defaultProjectTitle(v.Repo)
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("New GitHub project title").
			Description("A new Projects v2 board will be created for " + v.Owner + ".").
			Value(&title).
			Validate(huh.ValidateNotEmpty()),
	))
	if err := runForm(cmd, form); err != nil {
		return err
	}
	n, err := createGitHubProject(cmd.Context(), v.Owner, strings.TrimSpace(title), token)
	if err != nil {
		return err
	}
	v.Project = n
	fmt.Fprintf(cmd.OutOrStdout(), "Created GitHub project #%d\n", n)
	return nil
}

func defaultProjectTitle(repo string) string {
	name := strings.TrimSuffix(repo, ".git")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		return "Tasks"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func ensureGitHubProjectLinked(ctx context.Context, owner string, project int, repo, token string) error {
	repoOwner := repo
	if i := strings.Index(repo, "/"); i > 0 {
		repoOwner = repo[:i]
	}
	if !strings.EqualFold(owner, repoOwner) {
		return fmt.Errorf("GitHub only links projects owned by %s to %s", repoOwner, repo)
	}
	if numbers, err := githubRepoProjectNumbers(ctx, repo, token); err == nil {
		for _, n := range numbers {
			if n == project {
				return nil
			}
		}
	}
	return linkGitHubProject(ctx, owner, project, repo, token)
}

func githubRepoProjectNumbers(ctx context.Context, repo, token string) ([]int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "repo", "view", repo, "--json", "projectsV2")
	if token != "" {
		cmd.Env = append(os.Environ(), "GH_TOKEN="+token)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var resp struct {
		ProjectsV2 struct {
			Nodes []struct {
				Number int `json:"number"`
			} `json:"Nodes"`
		} `json:"projectsV2"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, err
	}
	numbers := make([]int, 0, len(resp.ProjectsV2.Nodes))
	for _, n := range resp.ProjectsV2.Nodes {
		numbers = append(numbers, n.Number)
	}
	return numbers, nil
}

func linkGitHubProject(ctx context.Context, owner string, project int, repo, token string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "project", "link", strconv.Itoa(project),
		"--owner", owner, "--repo", repo)
	if token != "" {
		cmd.Env = append(os.Environ(), "GH_TOKEN="+token)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh project link: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func createGitHubProject(ctx context.Context, owner, title, token string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "project", "create",
		"--owner", owner, "--title", title, "--format", "json")
	if token != "" {
		cmd.Env = append(os.Environ(), "GH_TOKEN="+token)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("gh project create: %s", strings.TrimSpace(string(out)))
	}
	var resp struct {
		Number int `json:"number"`
	}
	if json.Unmarshal(out, &resp) == nil && resp.Number > 0 {
		return resp.Number, nil
	}
	projects, listErr := listGitHubProjects(ctx, owner, token)
	if listErr == nil {
		for _, p := range projects {
			if p.Title == title {
				return p.Number, nil
			}
		}
	}
	return 0, errors.New("project was created but its number could not be determined")
}

func (a *App) linearTeamOptions(ctx context.Context, v *initValues) []huh.Option[string] {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	teams, err := linear.ListTeams(ctx, linear.Config{
		Team:          v.Team,
		OAuthClientID: v.OAuthClientID,
		TokenEnv:      v.TokenEnv,
		ReadyStatus:   v.ReadyStatus,
	})
	if err != nil || len(teams) == 0 {
		return nil
	}
	sort.Slice(teams, func(i, j int) bool { return teams[i].Name < teams[j].Name })
	options := make([]huh.Option[string], 0, len(teams)+1)
	found := false
	for _, t := range teams {
		options = append(options, huh.NewOption(t.Key+"  "+t.Name, t.Key))
		if t.Key == v.Team {
			found = true
		}
	}
	if v.Team != "" && !found {
		options = append(options, huh.NewOption(v.Team+"  current team", v.Team))
	}
	if v.Team == "" {
		v.Team = teams[0].Key
	}
	return options
}

func fillGitHubDefaults(ctx context.Context, s *config.Settings, v *initValues) {
	if v.Owner == "" {
		v.Owner = s.Config.GitHub.Owner
	}
	if v.Repo == "" {
		v.Repo = s.Config.GitHub.Repo
	}
	if v.Project == 0 {
		v.Project = s.Config.GitHub.Project
	}
	v.StatusField = defaultValue(v.StatusField, defaultValue(s.Config.GitHub.StatusField, "Status"))
	v.PriorityField = defaultValue(v.PriorityField, defaultValue(s.Config.GitHub.PriorityField, "Priority"))
	if v.Owner == "" || v.Repo == "" {
		owner, repo := detectGitHubRepo(ctx)
		if v.Owner == "" {
			v.Owner = owner
		}
		if v.Repo == "" {
			v.Repo = repo
		}
	}
	if v.Owner == "" {
		v.Owner = ghLogin(ctx)
	}
	v.Repo = normalizeRepo(v.Owner, v.Repo)
}

func normalizeRepo(owner, repo string) string {
	repo = strings.TrimSpace(repo)
	if repo == "" || strings.Contains(repo, "/") {
		return repo
	}
	if owner == "" {
		return repo
	}
	return owner + "/" + repo
}

type githubProject struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
}

func listGitHubProjects(ctx context.Context, owner, token string) ([]githubProject, error) {
	if owner == "" {
		return nil, errors.New("github owner is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "project", "list",
		"--owner", owner, "--format", "json", "--limit", "100")
	if token != "" {
		cmd.Env = append(os.Environ(), "GH_TOKEN="+token)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var resp struct {
		Projects []githubProject `json:"projects"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, err
	}
	return resp.Projects, nil
}

func detectGitHubRepo(ctx context.Context) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "git", "remote", "get-url", "origin").Output(); err == nil {
		if owner, repo := parseGitHubRepo(string(out)); owner != "" {
			return owner, repo
		}
	}
	if out, err := exec.CommandContext(ctx, "gh", "repo", "view", "--json", "owner,name").Output(); err == nil {
		var resp struct {
			Owner struct {
				Login string `json:"login"`
			} `json:"owner"`
			Name string `json:"name"`
		}
		if json.Unmarshal(out, &resp) == nil && resp.Owner.Login != "" {
			return resp.Owner.Login, resp.Name
		}
	}
	return "", ""
}

func parseGitHubRepo(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, ".git")
	if rest, ok := strings.CutPrefix(raw, "git@github.com:"); ok {
		parts := strings.Split(rest, "/")
		if len(parts) >= 2 {
			return parts[0], parts[1]
		}
	}
	if u, err := url.Parse(raw); err == nil && strings.EqualFold(u.Host, "github.com") {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 2 {
			return parts[0], parts[1]
		}
	}
	return "", ""
}

func ghLogin(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gh", "api", "user", "--jq", ".login").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func defaultValue(value, def string) string {
	if strings.TrimSpace(value) == "" {
		return def
	}
	return value
}

func (a *App) backendChoices(current string) ([]string, []string) {
	var names, labels []string
	seen := map[string]bool{}
	add := func(name, label string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
		labels = append(labels, label)
	}
	for _, name := range native.Names() {
		switch name {
		case "sqlite":
			add(name, "sqlite  local board, default")
		case "github":
			add(name, "github  GitHub Projects")
		case "linear":
			add(name, "linear  Linear team")
		default:
			add(name, name)
		}
	}
	if current != "" {
		add(current, current+"  configured backend")
	}
	if a.manager != nil {
		external, _ := a.manager.List()
		for _, plugin := range external {
			add(plugin.Name, plugin.Name+"  external plugin")
		}
	}
	return names, labels
}
