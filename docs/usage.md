# Usage

```zsh
go run cmd/nononsec/main.go
```

```zsh
curl -L https://github.com/030/nononsec/releases/download/v0.1.0-rc.1/nononsec-v0.1.0-rc.1-linux-amd64 \
  -o nononsec && \
  chmod +x nononsec && \
  sudo mv nononsec /usr/local/bin/nononsec
```

```zsh
go install github.com/030/nononsec/cmd/nononsec@v0.1.0-rc.1
```

```zsh
nononsec
```

Output:

```zsh
INFO[0000] Detected project type: go
```
