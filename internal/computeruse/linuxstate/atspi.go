package linuxstate

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"unicode"

	"github.com/tmc/axmcp/internal/computeruse"
)

const (
	atspiBusName       = "org.a11y.Bus"
	atspiBusPath       = "/org/a11y/bus"
	atspiRegistryBus   = "org.a11y.atspi.Registry"
	atspiRootPath      = "/org/a11y/atspi/accessible/root"
	atspiAccessible    = "org.a11y.atspi.Accessible"
	atspiAction        = "org.a11y.atspi.Action"
	atspiComponent     = "org.a11y.atspi.Component"
	atspiEditableText  = "org.a11y.atspi.EditableText"
	atspiValue         = "org.a11y.atspi.Value"
	dbusProperties     = "org.freedesktop.DBus.Properties"
	maxATSPIDepth      = 10
	maxATSPINodes      = 512
	atspiStateEditable = 7
	atspiStateEnabled  = 8
)

type atspiRef struct {
	Bus  string
	Path string
}

type atspiReader struct {
	env      func(string) string
	lookPath func(string) (string, error)
	run      func(context.Context, string, ...string) ([]byte, error)

	command string
	address string
	nodes   int
}

func readAccessibilityTree(ctx context.Context, win Window) (AccessibilityNode, error) {
	return defaultATSPIReader().readWindow(ctx, win)
}

func performATSPIAction(ctx context.Context, action accessibilityAction) error {
	return defaultATSPIReader().performAction(ctx, action)
}

func setATSPIValue(ctx context.Context, value accessibilityValue) error {
	return defaultATSPIReader().setValue(ctx, value)
}

func defaultATSPIReader() *atspiReader {
	return &atspiReader{
		env:      os.Getenv,
		lookPath: exec.LookPath,
		run:      runATSPICommand,
	}
}

func runATSPICommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func (r *atspiReader) readWindow(ctx context.Context, win Window) (AccessibilityNode, error) {
	if err := ctx.Err(); err != nil {
		return AccessibilityNode{}, err
	}
	if strings.TrimSpace(win.ID) == "" {
		return AccessibilityNode{}, fmt.Errorf("missing X11 window id")
	}
	if err := r.configure(ctx); err != nil {
		return AccessibilityNode{}, err
	}

	apps, err := r.children(ctx, atspiRef{Bus: atspiRegistryBus, Path: atspiRootPath})
	if err != nil {
		return AccessibilityNode{}, atspiUnavailable("enumerate AT-SPI applications", err)
	}
	ref, err := r.findWindow(ctx, win, apps)
	if err != nil {
		return AccessibilityNode{}, err
	}
	node, err := r.readNode(ctx, win, ref, 0)
	if err != nil {
		return AccessibilityNode{}, atspiUnavailable("read AT-SPI node", err)
	}
	return node, nil
}

func (r *atspiReader) configure(ctx context.Context) error {
	env := r.env
	if env == nil {
		env = os.Getenv
	}
	if strings.TrimSpace(env("DBUS_SESSION_BUS_ADDRESS")) == "" {
		return computeruse.PlatformUnsupported("read AT-SPI tree: DBUS_SESSION_BUS_ADDRESS is not set")
	}
	noBridge := strings.TrimSpace(env("NO_AT_BRIDGE"))
	if noBridge == "1" || strings.EqualFold(noBridge, "true") {
		return computeruse.PlatformUnsupported("read AT-SPI tree: NO_AT_BRIDGE disables the AT-SPI bridge")
	}
	lookPath := r.lookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	command, err := lookPath("gdbus")
	if err != nil {
		return computeruse.PlatformUnsupported("read AT-SPI tree: gdbus is not available on PATH")
	}
	r.command = command

	address, err := r.busAddress(ctx)
	if err != nil {
		return atspiUnavailable("get AT-SPI bus address", err)
	}
	r.address = address
	return nil
}

func (r *atspiReader) busAddress(ctx context.Context) (string, error) {
	out, err := r.call(ctx, "", atspiBusName, atspiBusPath, atspiBusName+".GetAddress")
	if err != nil {
		return "", err
	}
	address := firstGVariantString(out)
	if strings.TrimSpace(address) == "" {
		return "", fmt.Errorf("empty AT-SPI bus address")
	}
	return address, nil
}

func (r *atspiReader) findWindow(ctx context.Context, win Window, apps []atspiRef) (atspiRef, error) {
	for _, app := range apps {
		candidates := []atspiRef{app}
		children, err := r.children(ctx, app)
		if err == nil {
			candidates = append(candidates, children...)
		}
		for _, ref := range candidates {
			node := r.readNodeMetadata(ctx, win, ref)
			if atspiMatchesWindow(win, node) {
				return ref, nil
			}
		}
	}
	return atspiRef{}, computeruse.PlatformUnsupported("read AT-SPI tree: no accessible window matches X11 window")
}

func (r *atspiReader) readNode(ctx context.Context, win Window, ref atspiRef, depth int) (AccessibilityNode, error) {
	if err := ctx.Err(); err != nil {
		return AccessibilityNode{}, err
	}
	if r.nodes >= maxATSPINodes {
		return AccessibilityNode{}, nil
	}
	r.nodes++

	node := r.readNodeMetadata(ctx, win, ref)
	if depth >= maxATSPIDepth {
		return node, nil
	}
	children, err := r.children(ctx, ref)
	if err != nil {
		return node, nil
	}
	for _, child := range children {
		if r.nodes >= maxATSPINodes {
			break
		}
		childNode, err := r.readNode(ctx, win, child, depth+1)
		if err != nil {
			return AccessibilityNode{}, err
		}
		if !childNode.isEmpty() {
			node.Children = append(node.Children, childNode)
		}
	}
	return node, nil
}

func (r *atspiReader) performAction(ctx context.Context, action accessibilityAction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ref := atspiRef{
		Bus:  strings.TrimSpace(action.Native.BusName),
		Path: strings.TrimSpace(action.Native.ObjectPath),
	}
	if ref.Bus == "" || ref.Path == "" {
		return computeruse.PlatformUnsupported("perform AT-SPI action")
	}
	action.Name = strings.TrimSpace(action.Name)
	if action.Name == "" {
		return fmt.Errorf("missing AT-SPI action")
	}
	if err := r.configure(ctx); err != nil {
		return err
	}
	index, err := r.actionIndex(ctx, ref, action.Name)
	if err != nil {
		return err
	}
	out, err := r.call(ctx, r.address, ref.Bus, ref.Path, atspiAction+".DoAction", strconv.Itoa(index))
	if err != nil {
		return fmt.Errorf("perform AT-SPI action %q: %w", action.Name, err)
	}
	if gvariantIsFalse(out) {
		return fmt.Errorf("perform AT-SPI action %q: action returned false", action.Name)
	}
	return nil
}

func (r *atspiReader) setValue(ctx context.Context, value accessibilityValue) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ref := atspiRef{
		Bus:  strings.TrimSpace(value.Native.BusName),
		Path: strings.TrimSpace(value.Native.ObjectPath),
	}
	if ref.Bus == "" || ref.Path == "" {
		return computeruse.PlatformUnsupported("set value with AT-SPI")
	}
	if err := r.configure(ctx); err != nil {
		return err
	}
	interfaces := r.interfaces(ctx, ref)
	if interfaces.has(atspiEditableText) {
		out, err := r.call(ctx, r.address, ref.Bus, ref.Path, atspiEditableText+".SetTextContents", quoteGVariantString(value.Value))
		if err != nil {
			return fmt.Errorf("set AT-SPI text contents: %w", err)
		}
		if gvariantIsFalse(out) {
			return fmt.Errorf("set AT-SPI text contents: action returned false")
		}
		return nil
	}
	if interfaces.has(atspiValue) {
		f, err := strconv.ParseFloat(strings.TrimSpace(value.Value), 64)
		if err != nil {
			return fmt.Errorf("set AT-SPI current value: parse %q as number: %w", value.Value, err)
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("set AT-SPI current value: invalid number %q", value.Value)
		}
		_, err = r.call(ctx, r.address, ref.Bus, ref.Path, dbusProperties+".Set",
			quoteGVariantString(atspiValue),
			quoteGVariantString("CurrentValue"),
			formatGVariantDoubleVariant(f),
		)
		if err != nil {
			return fmt.Errorf("set AT-SPI current value: %w", err)
		}
		return nil
	}
	return computeruse.PlatformUnsupported("set value with AT-SPI")
}

func (r *atspiReader) readNodeMetadata(ctx context.Context, win Window, ref atspiRef) AccessibilityNode {
	interfaces := r.interfaces(ctx, ref)
	states := r.states(ctx, ref)
	actions, settableByAction := r.actions(ctx, ref, interfaces)
	node := AccessibilityNode{
		Native: NativeElement{
			WindowID:   win.ID,
			BusName:    ref.Bus,
			ObjectPath: ref.Path,
		},
		Role:             r.role(ctx, ref),
		Title:            r.stringProperty(ctx, ref, atspiAccessible, "Name"),
		Description:      r.description(ctx, ref),
		Identifier:       r.stringProperty(ctx, ref, atspiAccessible, "AccessibleId"),
		Rect:             r.extents(ctx, ref, interfaces),
		Enabled:          states[atspiStateEnabled],
		Settable:         states[atspiStateEditable] || interfaces.has(atspiEditableText) || interfaces.has(atspiValue) || settableByAction,
		SecondaryActions: actions,
	}
	if interfaces.has(atspiValue) {
		node.Value = r.value(ctx, ref)
	}
	return node
}

func (r *atspiReader) actionIndex(ctx context.Context, ref atspiRef, name string) (int, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("missing AT-SPI action")
	}
	n, ok := r.intProperty(ctx, ref, atspiAction, "NActions")
	if !ok || n <= 0 {
		return 0, computeruse.PlatformUnsupported("perform AT-SPI action")
	}
	if n > 32 {
		n = 32
	}
	want := atspiActionNameKey(name)
	for i := range n {
		out, err := r.call(ctx, r.address, ref.Bus, ref.Path, atspiAction+".GetName", strconv.Itoa(i))
		if err != nil {
			continue
		}
		if atspiActionNameKey(firstGVariantString(out)) == want {
			return i, nil
		}
	}
	return 0, computeruse.PlatformUnsupported("perform AT-SPI action " + strconv.Quote(name))
}

func (r *atspiReader) call(ctx context.Context, address, dest, path, method string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	run := r.run
	if run == nil {
		run = runATSPICommand
	}
	command := r.command
	if command == "" {
		command = "gdbus"
	}
	gdbusArgs := []string{"call"}
	if address == "" {
		gdbusArgs = append(gdbusArgs, "--session")
	} else {
		gdbusArgs = append(gdbusArgs, "--address", address)
	}
	gdbusArgs = append(gdbusArgs, "--dest", dest, "--object-path", path, "--method", method)
	gdbusArgs = append(gdbusArgs, args...)
	out, err := run(ctx, command, gdbusArgs...)
	if err != nil {
		return nil, fmt.Errorf("gdbus %s: %w", method, err)
	}
	return out, nil
}

func (r *atspiReader) children(ctx context.Context, ref atspiRef) ([]atspiRef, error) {
	out, err := r.call(ctx, r.address, ref.Bus, ref.Path, atspiAccessible+".GetChildren")
	if err != nil {
		return nil, err
	}
	return parseATSPIRefs(out), nil
}

func (r *atspiReader) role(ctx context.Context, ref atspiRef) string {
	out, err := r.call(ctx, r.address, ref.Bus, ref.Path, atspiAccessible+".GetRoleName")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(firstGVariantString(out))
}

func (r *atspiReader) interfaces(ctx context.Context, ref atspiRef) atspiInterfaces {
	out, err := r.call(ctx, r.address, ref.Bus, ref.Path, atspiAccessible+".GetInterfaces")
	if err != nil {
		return nil
	}
	set := make(atspiInterfaces)
	for _, name := range gvariantStrings(out) {
		if strings.TrimSpace(name) != "" {
			set[strings.TrimSpace(name)] = true
		}
	}
	return set
}

func (r *atspiReader) states(ctx context.Context, ref atspiRef) map[int]bool {
	out, err := r.call(ctx, r.address, ref.Bus, ref.Path, atspiAccessible+".GetState")
	if err != nil {
		return nil
	}
	states := make(map[int]bool)
	for _, state := range gvariantInts(out) {
		states[state] = true
	}
	return states
}

func (r *atspiReader) stringProperty(ctx context.Context, ref atspiRef, iface, property string) string {
	out, err := r.call(ctx, r.address, ref.Bus, ref.Path, dbusProperties+".Get", quoteGVariantString(iface), quoteGVariantString(property))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(firstGVariantString(out))
}

func (r *atspiReader) intProperty(ctx context.Context, ref atspiRef, iface, property string) (int, bool) {
	out, err := r.call(ctx, r.address, ref.Bus, ref.Path, dbusProperties+".Get", quoteGVariantString(iface), quoteGVariantString(property))
	if err != nil {
		return 0, false
	}
	ints := gvariantInts(out)
	if len(ints) == 0 {
		return 0, false
	}
	return ints[0], true
}

func (r *atspiReader) description(ctx context.Context, ref atspiRef) string {
	description := r.stringProperty(ctx, ref, atspiAccessible, "Description")
	if description != "" {
		return description
	}
	return r.stringProperty(ctx, ref, atspiAccessible, "HelpText")
}

func (r *atspiReader) extents(ctx context.Context, ref atspiRef, interfaces atspiInterfaces) Rect {
	if !interfaces.has(atspiComponent) {
		return Rect{}
	}
	out, err := r.call(ctx, r.address, ref.Bus, ref.Path, atspiComponent+".GetExtents", "uint32 0")
	if err != nil {
		return Rect{}
	}
	ints := gvariantInts(out)
	if len(ints) < 4 {
		return Rect{}
	}
	return Rect{X: ints[0], Y: ints[1], Width: ints[2], Height: ints[3]}
}

func (r *atspiReader) actions(ctx context.Context, ref atspiRef, interfaces atspiInterfaces) ([]string, bool) {
	if !interfaces.has(atspiAction) {
		return nil, false
	}
	n, ok := r.intProperty(ctx, ref, atspiAction, "NActions")
	if !ok || n <= 0 {
		return nil, false
	}
	if n > 32 {
		n = 32
	}
	actions := make([]string, 0, n)
	settable := false
	for i := range n {
		out, err := r.call(ctx, r.address, ref.Bus, ref.Path, atspiAction+".GetName", strconv.Itoa(i))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(firstGVariantString(out))
		if name == "" {
			continue
		}
		switch atspiActionNameKey(name) {
		case "set value", "settext":
			settable = true
		}
		actions = append(actions, name)
	}
	return actions, settable
}

func (r *atspiReader) value(ctx context.Context, ref atspiRef) string {
	for _, property := range []string{"Text", "CurrentValue"} {
		out, err := r.call(ctx, r.address, ref.Bus, ref.Path, dbusProperties+".Get", quoteGVariantString(atspiValue), quoteGVariantString(property))
		if err != nil {
			continue
		}
		if value := strings.TrimSpace(firstGVariantString(out)); value != "" {
			return value
		}
		floats := gvariantFloats(out)
		if len(floats) > 0 {
			return strconv.FormatFloat(floats[0], 'f', -1, 64)
		}
	}
	return ""
}

type atspiInterfaces map[string]bool

func (m atspiInterfaces) has(name string) bool {
	if m == nil {
		return false
	}
	if m[name] {
		return true
	}
	for got := range m {
		if got == name || strings.HasSuffix(got, "."+strings.TrimPrefix(name, "org.a11y.atspi.")) {
			return true
		}
	}
	return false
}

func atspiMatchesWindow(win Window, node AccessibilityNode) bool {
	role := strings.ToLower(strings.TrimSpace(node.Role))
	if role != "frame" && role != "window" && role != "dialog" && role != "alert" {
		return false
	}
	title := strings.ToLower(strings.TrimSpace(node.Title))
	wantTitle := strings.ToLower(strings.TrimSpace(win.Title))
	if title != "" && wantTitle != "" && !strings.Contains(title, wantTitle) && !strings.Contains(wantTitle, title) {
		return false
	}
	return rectCloseToWindow(win, node.Rect)
}

func rectCloseToWindow(win Window, rect Rect) bool {
	if rect.Width <= 0 || rect.Height <= 0 {
		return false
	}
	const edgeTolerance = 32
	if absInt(rect.X-win.X) <= edgeTolerance &&
		absInt(rect.Y-win.Y) <= edgeTolerance &&
		absInt(rect.Width-win.Width) <= edgeTolerance &&
		absInt(rect.Height-win.Height) <= edgeTolerance {
		return true
	}
	return rectOverlapArea(win, rect)*2 >= win.Width*win.Height
}

func rectOverlapArea(win Window, rect Rect) int {
	left := maxInt(win.X, rect.X)
	top := maxInt(win.Y, rect.Y)
	right := minInt(win.X+win.Width, rect.X+rect.Width)
	bottom := minInt(win.Y+win.Height, rect.Y+rect.Height)
	if right <= left || bottom <= top {
		return 0
	}
	return (right - left) * (bottom - top)
}

func parseATSPIRefs(out []byte) []atspiRef {
	values := gvariantStrings(out)
	refs := make([]atspiRef, 0, len(values)/2)
	for i := 0; i+1 < len(values); i += 2 {
		ref := atspiRef{Bus: values[i], Path: values[i+1]}
		if strings.TrimSpace(ref.Bus) == "" || !strings.HasPrefix(ref.Path, "/") || strings.HasSuffix(ref.Path, "/null") {
			continue
		}
		refs = append(refs, ref)
	}
	return refs
}

func firstGVariantString(out []byte) string {
	values := gvariantStrings(out)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func gvariantStrings(out []byte) []string {
	s := string(out)
	var values []string
	for i := 0; i < len(s); i++ {
		if s[i] != '\'' {
			continue
		}
		var b strings.Builder
		i++
		for i < len(s) {
			if s[i] == '\\' && i+1 < len(s) {
				i++
				b.WriteByte(s[i])
				i++
				continue
			}
			if s[i] == '\'' {
				break
			}
			b.WriteByte(s[i])
			i++
		}
		values = append(values, b.String())
	}
	return values
}

func gvariantInts(out []byte) []int {
	var values []int
	for _, token := range gvariantNumberTokens(string(out)) {
		if strings.ContainsAny(token, ".eE") {
			continue
		}
		value, err := strconv.Atoi(token)
		if err == nil {
			values = append(values, value)
		}
	}
	return values
}

func gvariantFloats(out []byte) []float64 {
	var values []float64
	for _, token := range gvariantNumberTokens(string(out)) {
		value, err := strconv.ParseFloat(token, 64)
		if err == nil {
			values = append(values, value)
		}
	}
	return values
}

func gvariantIsFalse(out []byte) bool {
	for _, token := range strings.FieldsFunc(strings.ToLower(string(out)), func(r rune) bool {
		return !unicode.IsLetter(r)
	}) {
		if token == "false" {
			return true
		}
	}
	return false
}

func gvariantNumberTokens(s string) []string {
	var tokens []string
	for i := 0; i < len(s); {
		if s[i] == '\'' {
			i++
			for i < len(s) {
				if s[i] == '\\' && i+1 < len(s) {
					i += 2
					continue
				}
				if s[i] == '\'' {
					i++
					break
				}
				i++
			}
			continue
		}
		if !isNumberStart(s, i) {
			i++
			continue
		}
		start := i
		i++
		for i < len(s) && (isASCIIDigit(s[i]) || s[i] == '.' || s[i] == 'e' || s[i] == 'E' || s[i] == '+' || s[i] == '-') {
			i++
		}
		tokens = append(tokens, s[start:i])
	}
	return tokens
}

func isNumberStart(s string, i int) bool {
	if s[i] == '-' {
		return i+1 < len(s) && isASCIIDigit(s[i+1]) && (i == 0 || !isIdentifierByte(s[i-1]))
	}
	return isASCIIDigit(s[i]) && (i == 0 || !isIdentifierByte(s[i-1]))
}

func isASCIIDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func isIdentifierByte(b byte) bool {
	return b == '_' || isASCIIDigit(b) || unicode.IsLetter(rune(b))
}

func quoteGVariantString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	return "'" + s + "'"
}

func formatGVariantDoubleVariant(f float64) string {
	return "<double " + strconv.FormatFloat(f, 'g', -1, 64) + ">"
}

func atspiActionNameKey(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.NewReplacer("_", " ", "-", " ").Replace(name)
	return strings.Join(strings.Fields(name), " ")
}

func atspiUnavailable(action string, err error) error {
	if err == nil {
		return computeruse.PlatformUnsupported(action)
	}
	return fmt.Errorf("%s: %v: %w", action, err, computeruse.ErrPlatformUnsupported)
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
