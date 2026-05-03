# X Post Watcher

A small Go script that watches an X/Twitter account and sends a native macOS notification when there is a new post.

Built for simple personal monitoring, like watching a school, organization, or news account.

## Features

- Watch any public X/Twitter username
- Native macOS notifications
- Stores last seen post locally
- Avoids duplicate notifications
- Can ignore replies and reposts
- Can run once or keep polling
- Can stop automatically after the first notification

## Requirements

- macOS
- Go installed
- X Developer account
- X API Bearer Token
- X API credits for read requests

## Setup

Clone the repo:

```bash
git clone https://github.com/YOUR_USERNAME/x-post-watcher.git
cd x-post-watcher
```

Set your X API Bearer Token:

```bash
export X_BEARER_TOKEN="your_bearer_token_here"
```

To make it permanent on macOS/zsh:

```bash
echo 'export X_BEARER_TOKEN="your_bearer_token_here"' >> ~/.zshrc
source ~/.zshrc
```

## Usage

Watch an account:

```bash
go run main.go -user golang -interval 10m
```

Watch X Developers for testing:

```bash
go run main.go -user xdevelopers -interval 10m
```

Run once and exit:

```bash
go run main.go -user xdevelopers -once
```

Send a notification on the first run using the latest existing post:

```bash
go run main.go -user xdevelopers -once -notify-on-first-run
```

Stop after the first new-post notification:

```bash
go run main.go -user golang -interval 10m -stop-after-notify
```

Build a binary:

```bash
go build -o xpostwatch main.go
./xpostwatch -user golang -interval 10m
```

## Options

| Flag | Description | Default |
|---|---|---|
| `-user` | X/Twitter username, with or without `@` | required or project default |
| `-interval` | Polling interval, e.g. `2m`, `10m`, `30m` | `2m` |
| `-state` | Custom path for the state JSON file | `~/.xpostwatch/<username>.json` |
| `-include-replies` | Include replies | `false` |
| `-include-reposts` | Include reposts/retweets | `false` |
| `-notify-on-first-run` | Notify for latest existing posts on first run | `false` |
| `-once` | Check once and exit | `false` |
| `-max-results` | Number of posts to fetch per check | `5` |
| `-stop-after-notify` | Exit after sending a notification | `false` |

## How it works

The script:

1. Resolves the username to a numeric X user ID.
2. Fetches recent posts for that user.
3. Stores the newest post ID in a local JSON state file.
4. On later checks, asks only for posts newer than the last seen ID.
5. Sends a macOS notification when new posts are found.

State is stored at:

```bash
~/.xpostwatch/<username>.json
```

Delete that file if you want to reset the watcher.

## Cost note

The official X API uses paid read credits. This script is designed to reduce unnecessary reads by caching the user ID and using `since_id` after the first run.

For best results, use a reasonable interval like:

```bash
10m
```

or:

```bash
30m
```

Avoid very short intervals unless you are okay with using more API credits.

## Stopping the watcher

If the script is running in your terminal, press:

```bash
Ctrl + C
```

Or run with:

```bash
-stop-after-notify
```

to exit automatically after the first notification.

## Example

```bash
export X_BEARER_TOKEN="your_bearer_token_here"
go run main.go -user golang -interval 10m -stop-after-notify
```

## Notes

This project uses:

- X API v2 username lookup
- X API v2 user posts endpoint
- macOS `osascript` / AppleScript notifications

The script expects the `X_BEARER_TOKEN` environment variable to be set before running.

## Disclaimer

This project uses the official X API. You are responsible for your own API usage, billing, and compliance with X's developer terms.

## License

MIT