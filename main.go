package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const apiBase = "https://api.x.com/2"

type State struct {
	Username   string    `json:"username"`
	UserID     string    `json:"user_id"`
	LastSeenID string    `json:"last_seen_id"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type UserLookupResponse struct {
	Data *struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Username string `json:"username"`
	} `json:"data"`
	Errors []APIProblem `json:"errors"`
}

type TweetsResponse struct {
	Data []Tweet `json:"data"`
	Meta struct {
		NewestID    string `json:"newest_id"`
		ResultCount int    `json:"result_count"`
	} `json:"meta"`
	Errors []APIProblem `json:"errors"`
}

type Tweet struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}

type APIProblem struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

type HTTPStatusError struct {
	StatusCode int
	Body       string
	ResetAt    time.Time
}

func (e *HTTPStatusError) Error() string {
	msg := fmt.Sprintf("X API returned HTTP %d", e.StatusCode)
	if e.Body != "" {
		msg += ": " + e.Body
	}
	return msg
}

func main() {
	usernameFlag := flag.String("user", "", "X/Twitter username, with or without @")
	interval := flag.Duration("interval", 2*time.Minute, "polling interval, e.g. 30s, 2m, 10m")
	statePathFlag := flag.String("state", "", "state JSON path; default is ~/.xpostwatch/<username>.json")
	includeReplies := flag.Bool("include-replies", false, "notify for replies too")
	includeReposts := flag.Bool("include-reposts", false, "notify for reposts/retweets too")
	notifyFirstRun := flag.Bool("notify-on-first-run", false, "notify for existing latest posts on first run")
	once := flag.Bool("once", false, "check once and exit")
	maxResults := flag.Int("max-results", 5, "number of posts to fetch per check; X requires 5..100")
	flag.Parse()

	if *maxResults < 5 || *maxResults > 100 {
		fatalf("-max-results must be between 5 and 100")
	}

	bearer := os.Getenv("X_BEARER_TOKEN")
	if bearer == "" {
		bearer = os.Getenv("BEARER_TOKEN")
	}
	if bearer == "" {
		fatalf("set X_BEARER_TOKEN or BEARER_TOKEN first")
	}

	username, err := cleanUsername(*usernameFlag)
	if err != nil {
		fatalf("invalid -user: %v", err)
	}

	statePath := *statePathFlag
	if statePath == "" {
		statePath, err = defaultStatePath(username)
		if err != nil {
			fatalf("state path: %v", err)
		}
	}

	client := &http.Client{Timeout: 20 * time.Second}
	ctx := context.Background()

	state, _ := loadState(statePath)
	if state.UserID == "" || !strings.EqualFold(state.Username, username) {
		userID, err := lookupUserID(ctx, client, bearer, username)
		if err != nil {
			fatalf("lookup @%s: %v", username, err)
		}
		state.Username = username
		state.UserID = userID
	}

	fmt.Printf("Watching @%s every %s. State: %s\n", username, interval.String(), statePath)

	for {
		changedState, err := checkOnce(ctx, client, bearer, state, *includeReplies, *includeReposts, *notifyFirstRun, *maxResults)
		if err != nil {
			var httpErr *HTTPStatusError
			if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusTooManyRequests && !httpErr.ResetAt.IsZero() {
				sleep := time.Until(httpErr.ResetAt.Add(5 * time.Second))
				fmt.Fprintf(os.Stderr, "Rate limited; sleeping until %s\n", httpErr.ResetAt.Format(time.RFC3339))
				if sleep > 0 {
					time.Sleep(sleep)
				}
			} else {
				fmt.Fprintf(os.Stderr, "check failed: %v\n", err)
			}
		} else {
			state = changedState
			if err := saveState(statePath, state); err != nil {
				fmt.Fprintf(os.Stderr, "save state failed: %v\n", err)
			}
		}

		if *once {
			return
		}
		time.Sleep(*interval)
	}
}

func checkOnce(ctx context.Context, client *http.Client, bearer string, state State, includeReplies, includeReposts, notifyFirstRun bool, maxResults int) (State, error) {
	resp, err := getUserTweets(ctx, client, bearer, state.UserID, state.LastSeenID, includeReplies, includeReposts, maxResults)
	if err != nil {
		return state, err
	}

	newestID := resp.Meta.NewestID
	if newestID == "" && len(resp.Data) > 0 {
		newestID = resp.Data[0].ID
	}

	firstRun := state.LastSeenID == ""
	if firstRun && newestID != "" {
		state.LastSeenID = newestID
		state.UpdatedAt = time.Now()
		if !notifyFirstRun {
			fmt.Printf("Initialized at latest post %s; no notification on first run.\n", newestID)
			return state, nil
		}
	}

	if len(resp.Data) == 0 {
		fmt.Printf("%s no new posts\n", time.Now().Format(time.RFC3339))
		return state, nil
	}

	for i := len(resp.Data) - 1; i >= 0; i-- {
		t := resp.Data[i]
		link := fmt.Sprintf("https://x.com/%s/status/%s", state.Username, t.ID)
		body := truncateForNotification(t.Text, 180) + "\n" + link

		fmt.Printf("New post by @%s: %s\n%s\n", state.Username, t.Text, link)

		if err := notifyMac("New X post", "@"+state.Username, body); err != nil {
			fmt.Fprintf(os.Stderr, "notification failed: %v\n", err)
		}
	}

	if newestID != "" {
		state.LastSeenID = newestID
	}
	state.UpdatedAt = time.Now()
	return state, nil
}

func lookupUserID(ctx context.Context, client *http.Client, bearer, username string) (string, error) {
	u := fmt.Sprintf("%s/users/by/username/%s", apiBase, url.PathEscape(username))

	var out UserLookupResponse
	if err := getJSON(ctx, client, bearer, u, &out); err != nil {
		return "", err
	}
	if out.Data == nil || out.Data.ID == "" {
		return "", fmt.Errorf("no user found; API errors: %v", out.Errors)
	}
	return out.Data.ID, nil
}

func getUserTweets(ctx context.Context, client *http.Client, bearer, userID, sinceID string, includeReplies, includeReposts bool, maxResults int) (TweetsResponse, error) {
	u, _ := url.Parse(fmt.Sprintf("%s/users/%s/tweets", apiBase, url.PathEscape(userID)))
	q := u.Query()
	q.Set("max_results", fmt.Sprintf("%d", maxResults))
	q.Set("tweet.fields", "created_at")

	if sinceID != "" {
		q.Set("since_id", sinceID)
	}

	excludes := make([]string, 0, 2)
	if !includeReplies {
		excludes = append(excludes, "replies")
	}
	if !includeReposts {
		excludes = append(excludes, "retweets")
	}
	if len(excludes) > 0 {
		q.Set("exclude", strings.Join(excludes, ","))
	}

	u.RawQuery = q.Encode()

	var out TweetsResponse
	if err := getJSON(ctx, client, bearer, u.String(), &out); err != nil {
		return out, err
	}
	return out, nil
}

func getJSON(ctx context.Context, client *http.Client, bearer, endpoint string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("User-Agent", "xpostwatch-go/1.0")

	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &HTTPStatusError{
			StatusCode: res.StatusCode,
			Body:       strings.TrimSpace(string(body)),
			ResetAt:    parseRateLimitReset(res.Header.Get("x-rate-limit-reset")),
		}
	}

	if len(body) == 0 {
		return nil
	}

	return json.Unmarshal(body, dest)
}

func notifyMac(title, subtitle, message string) error {
	script := fmt.Sprintf(
		"display notification %s with title %s subtitle %s sound name \"Glass\"",
		appleScriptQuote(message),
		appleScriptQuote(title),
		appleScriptQuote(subtitle),
	)
	return exec.Command("osascript", "-e", script).Run()
}

func appleScriptQuote(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return "\"" + s + "\""
}

func truncateForNotification(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}

func cleanUsername(input string) (string, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", fmt.Errorf("provide -user, e.g. -user xdevelopers")
	}

	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", err
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			return "", fmt.Errorf("could not find username in URL")
		}
		s = parts[0]
	}

	s = strings.TrimPrefix(s, "@")

	valid := regexp.MustCompile(`^[A-Za-z0-9_]{1,15}$`)
	if !valid.MatchString(s) {
		return "", fmt.Errorf("username must match letters, numbers, underscore, max 15 chars")
	}

	return s, nil
}

func defaultStatePath(username string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".xpostwatch", username+".json"), nil
}

func loadState(path string) (State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}

	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, err
	}

	return s, nil
}

func saveState(path string, state State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, b, 0o600)
}

func parseRateLimitReset(v string) time.Time {
	if v == "" {
		return time.Time{}
	}

	var epoch int64
	_, err := fmt.Sscanf(v, "%d", &epoch)
	if err != nil || epoch <= 0 {
		return time.Time{}
	}

	return time.Unix(epoch, 0)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
