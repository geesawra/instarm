# instarm

Polls Instapaper for unread articles tagged `remarkable`, turns them into EPUBs, and uploads them to a reMarkable account. After a successful upload the article is archived on Instapaper so it isn't processed again.

## What you need

- Instapaper API credentials (consumer key/secret + email/password)
- reMarkable device token and user token
- Optional: a reMarkable folder ID to upload into

## Configure

Environment variables:

```sh
export KEY="<instapaper-consumer-key>"
export SECRET="<instapaper-consumer-secret>"
export EMAIL="<instapaper-email>"
# Instapaper account password:
export PASSWORD=<...>

export RMAPI_DEVICE_TOKEN="<rmapi-device-token>"
export RMAPI_USER_TOKEN="<rmapi-user-token>"

# Optional. Empty means upload to root.
export REMARKABLE_FOLDER_ID=""
```

## Get reMarkable tokens

Run the built-in setup flow:

```sh
go run . -setup
```

It asks for the one-time code from `https://my.remarkable.com/device/browser/connect`, then prints `RMAPI_DEVICE_TOKEN` and `RMAPI_USER_TOKEN`.

To find a folder ID, run:

```sh
go run . -list-folders
```

## Run

```sh
go run .
```

It checks Instapaper every second. If you want to change the interval, edit `main.go`.

## Docker

Prebuilt image: `ghcr.io/geesawra/instarm`

Build locally:

```sh
docker build -t instarm .
```

Or pull from GitHub Container Registry:

```sh
docker pull ghcr.io/geesawra/instarm:main
```

### Authenticate

Run setup to get your reMarkable tokens. This only needs `-it` so it can read the code you paste:

```sh
docker run -it --rm ghcr.io/geesawra/instarm:main -setup
```

With a locally built image:

```sh
docker run -it --rm instarm -setup
```

### List folders

Once you have tokens:

```sh
docker run -it --rm \
  -e RMAPI_DEVICE_TOKEN \
  -e RMAPI_USER_TOKEN \
  ghcr.io/geesawra/instarm:main -list-folders
```

### Run the sync loop

```sh
docker run -it --rm \
  -e KEY \
  -e SECRET \
  -e EMAIL \
  -e PASSWORD \
  -e RMAPI_DEVICE_TOKEN \
  -e RMAPI_USER_TOKEN \
  -e REMARKABLE_FOLDER_ID \
  ghcr.io/geesawra/instarm:main
```

If you built locally, replace `ghcr.io/geesawra/instarm:main` with `instarm`.

## Notes

- The user token expires, but the app refreshes it automatically using the device token.
- If `REMARKABLE_FOLDER_ID` is set to a path like `Articles/Read Later`, the app resolves it to the folder ID. IDs work too.
- The uploaded document name matches the article title.
