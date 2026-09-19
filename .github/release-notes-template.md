## ragflow-x {{ .TagName }}

{{ if .PreviousTag }}自 **{{ .PreviousTag }}** 以来的变更（共 {{ len .Commits }} 个提交）：{{ else }}🎉 首次发版{{ end }}

{{ range .Commits -}}
- {{ .MessageHeadline }}{{ if .Author.Login }}（[@{{ .Author.Login }}]({{ .URL }})）{{ end }}
{{ end }}

---

### 安装

```bash
# Linux (amd64)
tar xzf ragflow-x-{{ .TagName }}-linux-amd64.tar.gz
./ragflow-x-linux-amd64

# Docker (coming soon)
docker pull ghcr.io/ragflow-x/ragflow-x:{{ .TagName }}
```

### 校验

```bash
sha256sum -c SHA256SUMS
```

---

_本说明由 GitHub Actions 自动生成（`release.yml` → `gh release create --notes-template`），资产清单见下方 Assets。_
