package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type config struct {
	root  string
	title string
}

type report struct {
	Title       string            `json:"title"`
	Root        string            `json:"root"`
	GeneratedAt string            `json:"generated_at"`
	Dimensions  []dimensionReport `json:"dimensions"`
}

type dimensionReport struct {
	Name    string         `json:"name"`
	Buckets []bucketReport `json:"buckets"`
}

type bucketReport struct {
	Dimension    string        `json:"dimension"`
	Name         string        `json:"name"`
	Value        string        `json:"value,omitempty"`
	Index        int           `json:"index,omitempty"`
	Total        int           `json:"total,omitempty"`
	Dir          string        `json:"dir"`
	Command      string        `json:"command,omitempty"`
	ContactSheet string        `json:"contact_sheet,omitempty"`
	Video        string        `json:"video,omitempty"`
	Frames       []frameReport `json:"frames"`
}

type frameReport struct {
	File  string `json:"file"`
	Cycle int    `json:"cycle"`
	Step  int    `json:"step,omitempty"`
	Phase string `json:"phase"`
	Label string `json:"label,omitempty"`
}

type runMeta struct {
	Dimension string
	Value     string
	Index     int
	Total     int
	Command   string
}

type cycleGroup struct {
	PhaseFrames []frameReport
	Steps       []stepGroup
}

type stepGroup struct {
	Step   int
	Label  string
	Frames []frameReport
}

var framePattern = regexp.MustCompile(`^cycle-(\d+)(?:-step-(\d+))?-(phase-start|before-click|after-click|idle-start|idle-mid|idle-end|phase-end)(?:-([a-z0-9-]+))?\.png$`)

var phaseOrder = map[string]int{
	"phase-start":  0,
	"before-click": 1,
	"after-click":  2,
	"idle-start":   3,
	"idle-mid":     4,
	"idle-end":     5,
	"phase-end":    6,
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.root, "root", "", "sweep root directory")
	flag.StringVar(&cfg.title, "title", "Calc Click Sweep Review", "report title")
	flag.Parse()

	if strings.TrimSpace(cfg.root) == "" {
		fmt.Fprintln(os.Stderr, "calc-click-report: -root is required")
		os.Exit(2)
	}

	root, err := filepath.Abs(cfg.root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "calc-click-report:", err)
		os.Exit(1)
	}
	cfg.root = root
	if err := validateRoot(cfg.root); err != nil {
		fmt.Fprintln(os.Stderr, "calc-click-report:", err)
		os.Exit(1)
	}

	rep, err := buildReport(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "calc-click-report:", err)
		os.Exit(1)
	}
	if err := writeArtifacts(cfg.root, rep); err != nil {
		fmt.Fprintln(os.Stderr, "calc-click-report:", err)
		os.Exit(1)
	}

	fmt.Printf("saved review %s\n", filepath.Join(cfg.root, "index.md"))
	fmt.Printf("saved manifest %s\n", filepath.Join(cfg.root, "review.json"))
}

func buildReport(cfg config) (report, error) {
	entries, err := os.ReadDir(cfg.root)
	if err != nil {
		return report{}, fmt.Errorf("read root %s: %w", cfg.root, err)
	}

	rep := report{
		Title:       cfg.title,
		Root:        cfg.root,
		GeneratedAt: time.Now().Format(time.RFC3339),
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dim, err := buildDimension(cfg.root, entry.Name())
		if err != nil {
			return report{}, err
		}
		if len(dim.Buckets) == 0 {
			continue
		}
		rep.Dimensions = append(rep.Dimensions, dim)
	}

	sort.Slice(rep.Dimensions, func(i, j int) bool {
		return rep.Dimensions[i].Name < rep.Dimensions[j].Name
	})
	if len(rep.Dimensions) == 0 {
		return report{}, fmt.Errorf("no sweep buckets found under %s", cfg.root)
	}
	return rep, nil
}

func buildDimension(root, name string) (dimensionReport, error) {
	dir := filepath.Join(root, name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return dimensionReport{}, fmt.Errorf("read dimension %s: %w", dir, err)
	}
	dim := dimensionReport{Name: name}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		bucket, err := buildBucket(root, name, entry.Name())
		if err != nil {
			return dimensionReport{}, err
		}
		if len(bucket.Frames) == 0 {
			continue
		}
		dim.Buckets = append(dim.Buckets, bucket)
	}
	sort.Slice(dim.Buckets, func(i, j int) bool {
		if dim.Buckets[i].Index != dim.Buckets[j].Index {
			return dim.Buckets[i].Index < dim.Buckets[j].Index
		}
		return dim.Buckets[i].Name < dim.Buckets[j].Name
	})
	return dim, nil
}

func buildBucket(root, dimension, bucketName string) (bucketReport, error) {
	dir := filepath.Join(root, dimension, bucketName)
	meta, _ := readRunMeta(filepath.Join(dir, "run.meta"))

	entries, err := os.ReadDir(dir)
	if err != nil {
		return bucketReport{}, fmt.Errorf("read bucket %s: %w", dir, err)
	}

	bucket := bucketReport{
		Dimension: dimension,
		Name:      bucketName,
		Value:     meta.Value,
		Index:     meta.Index,
		Total:     meta.Total,
		Dir:       relOrAbs(root, dir),
		Command:   meta.Command,
	}

	files := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".png") {
			continue
		}
		if entry.Name() == "contact-sheet.png" {
			continue
		}
		frame, ok := parseFrameName(entry.Name())
		if !ok {
			continue
		}
		frame.File = relOrAbs(root, filepath.Join(dir, entry.Name()))
		bucket.Frames = append(bucket.Frames, frame)
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	sort.Slice(bucket.Frames, func(i, j int) bool {
		return compareFrames(bucket.Frames[i], bucket.Frames[j]) < 0
	})
	sort.Slice(files, func(i, j int) bool {
		a, _ := parseFrameName(filepath.Base(files[i]))
		b, _ := parseFrameName(filepath.Base(files[j]))
		return compareFrames(a, b) < 0
	})
	if bucket.Value == "" {
		bucket.Value = fallbackBucketValue(bucketName)
	}
	if bucket.Index == 0 {
		bucket.Index = bucketIndexFromName(bucketName)
	}
	if len(files) > 0 {
		sheet := filepath.Join(dir, "contact-sheet.png")
		if err := writeImageGrid(sheet, files, 4); err != nil {
			return bucketReport{}, fmt.Errorf("contact sheet %s: %w", sheet, err)
		}
		bucket.ContactSheet = relOrAbs(root, sheet)
	}
	if _, err := os.Stat(filepath.Join(dir, "run.mp4")); err == nil {
		bucket.Video = relOrAbs(root, filepath.Join(dir, "run.mp4"))
	}
	if err := writeBucketManifest(dir, bucket); err != nil {
		return bucketReport{}, err
	}
	return bucket, nil
}

func writeArtifacts(root string, rep report) error {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal review: %w", err)
	}
	if err := os.WriteFile(filepath.Join(root, "review.json"), data, 0o644); err != nil {
		return fmt.Errorf("write review.json: %w", err)
	}
	md := renderMarkdown(rep)
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte(md), 0o644); err != nil {
		return fmt.Errorf("write index.md: %w", err)
	}
	return nil
}

func writeBucketManifest(dir string, bucket bucketReport) error {
	data, err := json.MarshalIndent(bucket, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal bucket manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Join(dir, "manifest.json"), err)
	}
	return nil
}

func readRunMeta(path string) (runMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return runMeta{}, err
	}
	var meta runMeta
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "dimension":
			meta.Dimension = value
		case "value":
			meta.Value = value
		case "index":
			meta.Index, _ = strconv.Atoi(value)
		case "total":
			meta.Total, _ = strconv.Atoi(value)
		case "command":
			meta.Command = value
		}
	}
	return meta, nil
}

func parseFrameName(name string) (frameReport, bool) {
	m := framePattern.FindStringSubmatch(name)
	if m == nil {
		return frameReport{}, false
	}
	cycle, err := strconv.Atoi(m[1])
	if err != nil {
		return frameReport{}, false
	}
	step := 0
	if m[2] != "" {
		step, err = strconv.Atoi(m[2])
		if err != nil {
			return frameReport{}, false
		}
	}
	return frameReport{
		Cycle: cycle,
		Step:  step,
		Phase: m[3],
		Label: unslugLabel(m[4]),
	}, true
}

func unslugLabel(s string) string {
	switch s {
	case "":
		return ""
	case "plus-minus":
		return "+/-"
	case "equals":
		return "="
	case "percent":
		return "%"
	case "point":
		return "."
	case "plus":
		return "+"
	case "minus":
		return "-"
	case "times":
		return "times"
	case "divide":
		return "divide"
	default:
		return strings.ReplaceAll(s, "-", " ")
	}
}

func compareFrames(a, b frameReport) int {
	aGroup, aStep, aPhase := frameSortKey(a)
	bGroup, bStep, bPhase := frameSortKey(b)
	switch {
	case a.Cycle != b.Cycle:
		return cmpInt(a.Cycle, b.Cycle)
	case aGroup != bGroup:
		return cmpInt(aGroup, bGroup)
	case aStep != bStep:
		return cmpInt(aStep, bStep)
	case aPhase != bPhase:
		return cmpInt(aPhase, bPhase)
	case a.Label != b.Label:
		if a.Label < b.Label {
			return -1
		}
		return 1
	default:
		return 0
	}
}

func frameSortKey(frame frameReport) (group, step, phase int) {
	switch {
	case frame.Step == 0 && frame.Phase == "phase-start":
		return 0, 0, phaseOrder[frame.Phase]
	case frame.Step > 0:
		return 1, frame.Step, phaseOrder[frame.Phase]
	default:
		return 2, frame.Step, phaseOrder[frame.Phase]
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func bucketIndexFromName(name string) int {
	prefix, _, ok := strings.Cut(name, "-")
	if !ok {
		return 0
	}
	n, _ := strconv.Atoi(prefix)
	return n
}

func fallbackBucketValue(name string) string {
	_, rest, ok := strings.Cut(name, "-")
	if !ok || rest == "" {
		return name
	}
	if strings.HasSuffix(rest, "ms") {
		return rest
	}
	return strings.ReplaceAll(rest, "-", ".")
}

func relOrAbs(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return filepath.ToSlash(rel)
}

func renderMarkdown(rep report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", rep.Title)
	fmt.Fprintf(&b, "- Generated: `%s`\n", rep.GeneratedAt)
	fmt.Fprintf(&b, "- Root: `%s`\n", rep.Root)
	fmt.Fprintf(&b, "- Manifest: [review.json](%s)\n", markdownPath("review.json"))
	fmt.Fprintf(&b, "- Render: `md2html index.md > index.html`\n\n")

	b.WriteString("## Dimensions\n\n")
	for _, dim := range rep.Dimensions {
		fmt.Fprintf(&b, "- [%s](#%s)\n", dim.Name, anchor(dim.Name))
	}
	b.WriteString("\n")

	for _, dim := range rep.Dimensions {
		fmt.Fprintf(&b, "## %s\n\n", dim.Name)
		for _, bucket := range dim.Buckets {
			title := bucket.Value
			if title == "" {
				title = bucket.Name
			}
			if bucket.Index > 0 && bucket.Total > 0 {
				fmt.Fprintf(&b, "### %02d/%02d %s\n\n", bucket.Index, bucket.Total, title)
			} else {
				fmt.Fprintf(&b, "### %s\n\n", title)
			}
			fmt.Fprintf(&b, "- Dir: `%s`\n", bucket.Dir)
			fmt.Fprintf(&b, "- Manifest: [%s/manifest.json](%s)\n", bucket.Name, markdownPath(filepath.ToSlash(filepath.Join(bucket.Dir, "manifest.json"))))
			if bucket.Command != "" {
				fmt.Fprintf(&b, "- Command: `%s`\n", escapeBackticks(bucket.Command))
			}
			if bucket.ContactSheet != "" {
				fmt.Fprintf(&b, "- Contact sheet: [contact-sheet.png](%s)\n\n", markdownPath(bucket.ContactSheet))
				fmt.Fprintf(&b, "![%s %s](%s)\n\n", dim.Name, title, markdownPath(bucket.ContactSheet))
			}
			if bucket.Video != "" {
				fmt.Fprintf(&b, "- Video: [run.mp4](%s)\n\n", markdownPath(bucket.Video))
				fmt.Fprintf(&b, "![run.mp4](%s)\n\n", markdownPath(bucket.Video))
			}
			renderBucketDetails(&b, bucket)
		}
	}
	return b.String()
}

func renderBucketDetails(b *strings.Builder, bucket bucketReport) {
	groups := groupFrames(bucket.Frames)
	cycles := make([]int, 0, len(groups))
	for cycle := range groups {
		cycles = append(cycles, cycle)
	}
	sort.Ints(cycles)
	for _, cycle := range cycles {
		group := groups[cycle]
		fmt.Fprintf(b, "#### Cycle %d\n\n", cycle)
		if len(group.PhaseFrames) > 0 {
			fmt.Fprintf(b, "**Cycle Frames**\n\n")
			renderFrameTable(b, group.PhaseFrames)
		}
		for _, step := range group.Steps {
			label := step.Label
			if label == "" {
				label = strconv.Itoa(step.Step)
			}
			fmt.Fprintf(b, "**Step %d · %s**\n\n", step.Step, label)
			renderFrameTable(b, step.Frames)
		}
	}
}

func renderFrameTable(b *strings.Builder, frames []frameReport) {
	if len(frames) == 0 {
		return
	}
	b.WriteString("|")
	for _, frame := range frames {
		fmt.Fprintf(b, " %s |", phaseHeading(frame.Phase))
	}
	b.WriteString("\n|")
	for range frames {
		b.WriteString(" --- |")
	}
	b.WriteString("\n|")
	for _, frame := range frames {
		fmt.Fprintf(b, " ![%s](%s) |", frameAlt(frame), markdownPath(frame.File))
	}
	b.WriteString("\n\n")
}

func markdownPath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	switch {
	case path == "":
		return path
	case strings.HasPrefix(path, "./"), strings.HasPrefix(path, "../"), strings.HasPrefix(path, "/"):
		return path
	case strings.Contains(path, "://"):
		return path
	default:
		return "./" + path
	}
}

func groupFrames(frames []frameReport) map[int]cycleGroup {
	out := make(map[int]cycleGroup)
	for _, frame := range frames {
		group := out[frame.Cycle]
		if frame.Step == 0 {
			group.PhaseFrames = append(group.PhaseFrames, frame)
		} else {
			found := false
			for i := range group.Steps {
				if group.Steps[i].Step == frame.Step {
					group.Steps[i].Frames = append(group.Steps[i].Frames, frame)
					if group.Steps[i].Label == "" && frame.Label != "" {
						group.Steps[i].Label = frame.Label
					}
					found = true
					break
				}
			}
			if !found {
				group.Steps = append(group.Steps, stepGroup{
					Step:   frame.Step,
					Label:  frame.Label,
					Frames: []frameReport{frame},
				})
			}
		}
		out[frame.Cycle] = group
	}

	for cycle, group := range out {
		sort.Slice(group.PhaseFrames, func(i, j int) bool {
			return compareFrames(group.PhaseFrames[i], group.PhaseFrames[j]) < 0
		})
		sort.Slice(group.Steps, func(i, j int) bool {
			return group.Steps[i].Step < group.Steps[j].Step
		})
		for i := range group.Steps {
			sort.Slice(group.Steps[i].Frames, func(a, b int) bool {
				return compareFrames(group.Steps[i].Frames[a], group.Steps[i].Frames[b]) < 0
			})
		}
		out[cycle] = group
	}
	return out
}

func frameAlt(frame frameReport) string {
	if frame.Label != "" {
		return fmt.Sprintf("cycle %d step %d %s %s", frame.Cycle, frame.Step, frame.Phase, frame.Label)
	}
	return fmt.Sprintf("cycle %d %s", frame.Cycle, frame.Phase)
}

func phaseHeading(phase string) string {
	if phase == "" {
		return ""
	}
	words := strings.Split(strings.ReplaceAll(phase, "-", " "), " ")
	for i, word := range words {
		if word == "" {
			continue
		}
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}

func anchor(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), " ", "-")
}

func escapeBackticks(s string) string {
	return strings.ReplaceAll(s, "`", "'")
}

func writeImageGrid(path string, files []string, columns int) error {
	if len(files) == 0 {
		return fmt.Errorf("no images to render")
	}
	if columns <= 0 {
		columns = 4
	}
	images := make([]image.Image, 0, len(files))
	maxW := 0
	maxH := 0
	for _, file := range files {
		img, err := decodePNGFile(file)
		if err != nil {
			return err
		}
		images = append(images, img)
		if img.Bounds().Dx() > maxW {
			maxW = img.Bounds().Dx()
		}
		if img.Bounds().Dy() > maxH {
			maxH = img.Bounds().Dy()
		}
	}
	rows := (len(images) + columns - 1) / columns
	sheet := image.NewRGBA(image.Rect(0, 0, columns*maxW, rows*maxH))
	draw.Draw(sheet, sheet.Bounds(), &image.Uniform{C: color.NRGBA{R: 37, G: 58, B: 82, A: 255}}, image.Point{}, draw.Src)
	for i, img := range images {
		col := i % columns
		row := i / columns
		cell := image.Rect(col*maxW, row*maxH, col*maxW+img.Bounds().Dx(), row*maxH+img.Bounds().Dy())
		draw.Draw(sheet, cell, img, img.Bounds().Min, draw.Src)
	}
	return encodePNG(path, sheet)
}

func decodePNGFile(path string) (image.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return img, nil
}

func encodePNG(path string, img image.Image) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return nil
}

func walkDirs(root string) ([]string, error) {
	dirs := []string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || path == root {
			return nil
		}
		dirs = append(dirs, path)
		return nil
	})
	return dirs, err
}

func collectBucketDirs(root string) ([]string, error) {
	dirs, err := walkDirs(root)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if strings.HasSuffix(strings.ToLower(entry.Name()), ".png") {
				out = append(out, dir)
				break
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func validateRoot(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("root is not a directory")
	}
	buckets, err := collectBucketDirs(root)
	if err != nil {
		return err
	}
	if len(buckets) == 0 {
		return fmt.Errorf("no png buckets found under %s", root)
	}
	return nil
}
