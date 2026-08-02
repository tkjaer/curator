# Development

Curator requires Go 1.26 or newer. Run commands from the repository root.

## Test

```sh
go test ./...
```

## Run the admin

Start the admin server with the repository directory as the content root:

```sh
go run . serve
```

Open <http://127.0.0.1:8080/>. On first launch, Curator creates `cms.db` and
`originals/` if they do not exist.

## Preview the generated site

Build the static output:

```sh
go run . build
```

Serve it from a second terminal:

```sh
cd output
python3 -m http.server 8081
```

Open <http://127.0.0.1:8081/>. The `output/` directory is generated and can be
deleted and rebuilt at any time.
