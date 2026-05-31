//go:build darwin

package ghostcursor

import (
	"bytes"
	"fmt"
	"math"
	"strings"

	"golang.org/x/sys/unix"
)

type palette struct {
	dotRed      float64
	dotGreen    float64
	dotBlue     float64
	haloRed     float64
	haloGreen   float64
	haloBlue    float64
	borderRed   float64
	borderGreen float64
	borderBlue  float64
}

type processRecord struct {
	pid  int
	ppid int
	name string
}

type harnessInfo struct {
	pid       int
	name      string
	kind      hostKind
	paletteID int
}

type hostKind int

const (
	hostUnknown hostKind = iota
	hostClaude
	hostCodex
)

var codexPalettes = []palette{
	colorPalette(0.87, 0.93, 1.00, 0.58, 0.77, 1.00),
	colorPalette(0.84, 0.95, 1.00, 0.48, 0.80, 0.98),
	colorPalette(0.90, 0.96, 1.00, 0.63, 0.82, 1.00),
	colorPalette(0.82, 0.91, 1.00, 0.52, 0.73, 0.98),
}

var claudePalettes = []palette{
	colorPalette(1.00, 0.44, 0.08, 1.00, 0.55, 0.18),
	colorPalette(0.97, 0.51, 0.14, 1.00, 0.63, 0.24),
	colorPalette(0.93, 0.41, 0.21, 0.99, 0.53, 0.31),
	colorPalette(0.99, 0.58, 0.20, 1.00, 0.69, 0.32),
}

var fallbackPalettes = []palette{
	colorPalette(0.16, 0.63, 0.67, 0.25, 0.74, 0.78),
	colorPalette(0.24, 0.55, 0.95, 0.35, 0.65, 1.00),
	colorPalette(0.54, 0.71, 0.30, 0.64, 0.80, 0.41),
	colorPalette(0.88, 0.47, 0.40, 0.96, 0.59, 0.52),
	colorPalette(0.91, 0.66, 0.23, 0.98, 0.77, 0.35),
}

func colorPalette(dotRed, dotGreen, dotBlue, borderRed, borderGreen, borderBlue float64) palette {
	return palette{
		dotRed:      dotRed,
		dotGreen:    dotGreen,
		dotBlue:     dotBlue,
		haloRed:     dotRed,
		haloGreen:   dotGreen,
		haloBlue:    dotBlue,
		borderRed:   borderRed,
		borderGreen: borderGreen,
		borderBlue:  borderBlue,
	}
}

func detectPalette(getpid func() int) palette {
	info := detectHarness(getpid)
	return paletteForID(paletteFamily(info.kind), info.paletteID)
}

func paletteForTheme(theme Theme, getpid func() int) palette {
	switch theme {
	case ThemeCodex:
		return paletteForID(codexPalettes, getpid())
	case ThemeClaude:
		return paletteForID(claudePalettes, getpid())
	case ThemeNeutral:
		return paletteForID([]palette{
			colorPalette(0.94, 0.97, 1.00, 0.58, 0.70, 0.86),
			colorPalette(0.95, 0.98, 1.00, 0.62, 0.74, 0.88),
		}, getpid())
	default:
		return detectPalette(getpid)
	}
}

func detectHostKind(getpid func() int) hostKind {
	return detectHarness(getpid).kind
}

func detectInfo(getpid func() int) Info {
	info := detectHarness(getpid)
	family := paletteFamily(info.kind)
	p := paletteForID(family, info.paletteID)
	return Info{
		Harness:      info.kind.String(),
		MatchName:    info.name,
		MatchPID:     info.pid,
		PaletteID:    info.paletteID,
		PaletteIndex: paletteIndex(family, info.paletteID),
		DotColor:     colorHex(p.dotRed, p.dotGreen, p.dotBlue),
		BorderColor:  colorHex(p.borderRed, p.borderGreen, p.borderBlue),
	}
}

func detectHarness(getpid func() int) harnessInfo {
	records := processAncestry(getpid)
	for _, record := range records {
		switch hostKindForProcessName(record.name) {
		case hostCodex:
			return harnessInfo{kind: hostCodex, pid: record.pid, name: record.name, paletteID: record.pid}
		case hostClaude:
			return harnessInfo{kind: hostClaude, pid: record.pid, name: record.name, paletteID: record.pid}
		}
	}
	if len(records) == 0 {
		return harnessInfo{}
	}
	if records[0].ppid > 1 {
		return harnessInfo{kind: hostUnknown, paletteID: records[0].ppid}
	}
	return harnessInfo{kind: hostUnknown, paletteID: records[0].pid}
}

func detectHostKindFromNames(names []string) hostKind {
	for _, name := range names {
		if kind := hostKindForProcessName(name); kind != hostUnknown {
			return kind
		}
	}
	return hostUnknown
}

func hostKindForProcessName(name string) hostKind {
	name = normalizeProcessName(name)
	switch {
	case name == "":
		return hostUnknown
	case strings.HasPrefix(name, "codex"):
		return hostCodex
	case strings.HasPrefix(name, "claude"):
		return hostClaude
	default:
		return hostUnknown
	}
}

func paletteForID(family []palette, id int) palette {
	if len(family) == 0 {
		return palette{}
	}
	return family[paletteIndex(family, id)]
}

func paletteIndex(family []palette, id int) int {
	if len(family) == 0 {
		return 0
	}
	if id < 0 {
		id = -id
	}
	return id % len(family)
}

func paletteFamily(kind hostKind) []palette {
	switch kind {
	case hostCodex:
		return codexPalettes
	case hostClaude:
		return claudePalettes
	default:
		return fallbackPalettes
	}
}

func processAncestry(getpid func() int) []processRecord {
	pid := getpid()
	if pid <= 0 {
		return nil
	}
	var records []processRecord
	seen := make(map[int]bool)
	for pid > 1 && !seen[pid] {
		seen[pid] = true
		name, ppid, err := processInfo(pid)
		if err != nil {
			break
		}
		records = append(records, processRecord{pid: pid, ppid: ppid, name: name})
		if ppid <= 1 {
			break
		}
		pid = ppid
	}
	return records
}

var processInfo = func(pid int) (name string, ppid int, err error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", 0, err
	}
	name = string(bytes.TrimRight(kp.Proc.P_comm[:], "\x00"))
	ppid = int(kp.Proc.P_oppid)
	return name, ppid, nil
}

func normalizeProcessName(name string) string {
	var out []rune
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
			out = append(out, r+'a'-'A')
		case r >= 'a' && r <= 'z':
			out = append(out, r)
		}
	}
	return string(out)
}

func (k hostKind) String() string {
	switch k {
	case hostCodex:
		return "codex"
	case hostClaude:
		return "claude"
	default:
		return "unknown"
	}
}

func colorHex(red, green, blue float64) string {
	return fmt.Sprintf("#%02x%02x%02x", colorByte(red), colorByte(green), colorByte(blue))
}

func colorByte(v float64) uint8 {
	switch {
	case v <= 0:
		return 0
	default:
		if v >= 1 {
			return 255
		}
		return uint8(math.Round(v * 255))
	}
}
