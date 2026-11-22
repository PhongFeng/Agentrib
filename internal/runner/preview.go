package runner

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/PhongFeng/Agentrib/internal/protocol"
)

const maxPreviewTotal = 32 << 20

var unsafeArtifactName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func previewPaths(stepID string) ([]string, string) {
	switch {
	case stepID == "producer" || stepID == "product":
		return []string{"docs/production/requirements.md", "docs/production/art.md", "docs/production/audio.md", "docs/production/development.md"}, ".md"
	case stepID == "art" || strings.HasSuffix(stepID, ":fix:art"):
		return []string{"assets/art"}, ".png .jpg .jpeg .gif .webp"
	case stepID == "audio" || strings.HasSuffix(stepID, ":fix:audio"):
		return []string{"assets/audio/bgm", "assets/audio/sfx"}, ".wav .ogg .mp3 .flac .m4a .opus"
	default:
		return nil, ""
	}
}

func capturePreviews(ctx context.Context, checkout, dir, revision, stepID, output string) ([]protocol.Artifact, error) {
	paths, extensions := previewPaths(stepID)
	if len(paths) == 0 {
		return nil, nil
	}
	engineDir, err := filepath.Rel(checkout, dir)
	if err != nil {
		return nil, err
	}
	for i := range paths {
		paths[i] = path.Join(filepath.ToSlash(engineDir), paths[i])
	}
	entries, err := git(ctx, checkout, append([]string{"ls-tree", "-rlz", revision, "--"}, paths...)...)
	if err != nil {
		return nil, err
	}
	var previews []protocol.Artifact
	var total int64
	for _, entry := range strings.Split(entries, "\x00") {
		meta, source, _ := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if len(fields) != 4 || fields[0] != "100644" && fields[0] != "100755" {
			continue
		}
		basename := path.Base(source)
		ext := path.Ext(basename)
		if ext == "" || !strings.Contains(" "+extensions+" ", " "+strings.ToLower(ext)+" ") {
			continue
		}
		size, err := strconv.ParseInt(fields[3], 10, 64)
		limit := int64(protocol.MaxPreviewBytes)
		if strings.EqualFold(ext, ".md") {
			limit = 512 << 10
		}
		if err != nil || size < 1 || size > limit || total+size > maxPreviewTotal {
			continue
		}
		data, err := command(ctx, checkout, "git", "cat-file", "blob", fields[2]).Output()
		if err != nil {
			return previews, err
		}
		stem := unsafeArtifactName.ReplaceAllString(strings.TrimSuffix(basename, ext), "-")
		name := fmt.Sprintf("%s%02d-%s%s", protocol.PreviewArtifactPrefix, len(previews)+1, stem[:min(len(stem), 120)], strings.ToLower(ext))
		if err := os.MkdirAll(output, 0700); err != nil {
			return previews, err
		}
		target := filepath.Join(output, name)
		if err := os.WriteFile(target, data, 0600); err != nil {
			return previews, err
		}
		artifact, err := identify(target)
		if err != nil {
			return previews, err
		}
		previews = append(previews, artifact)
		total += size
		if len(previews) == protocol.MaxPreviewArtifacts {
			break
		}
	}
	return previews, nil
}
