package nodeid

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hwAddr 构造一个 net.HardwareAddr
func hwAddr(t *testing.T, s string) net.HardwareAddr {
	t.Helper()
	a, err := net.ParseMAC(s)
	if err != nil {
		t.Fatalf("ParseMAC %q: %v", s, err)
	}
	return a
}

func TestSelectHardwareMAC_PrefersPhysicalOverVirtual(t *testing.T) {
	// 物理网卡 ens18 应优先于按字母序排在前面的 Docker bridge
	oldPhys := isPhysicalNICBySysfs
	isPhysicalNICBySysfs = func(name string) bool { return name == "ens18" }
	t.Cleanup(func() { isPhysicalNICBySysfs = oldPhys })

	ifaces := []net.Interface{
		{Name: "br-52ae2a427de6", Flags: net.FlagUp, HardwareAddr: hwAddr(t, "8a:19:95:96:ef:c9")},
		{Name: "docker0", Flags: net.FlagUp, HardwareAddr: hwAddr(t, "1e:bf:53:11:b9:f8")},
		{Name: "ens18", Flags: net.FlagUp, HardwareAddr: hwAddr(t, "fe:fc:fe:a9:ca:14")},
	}
	got := selectHardwareMAC(ifaces)
	if got != "fe:fc:fe:a9:ca:14" {
		t.Fatalf("expected physical ens18 MAC, got %q", got)
	}
}

func TestSelectHardwareMAC_SkipsVirtualWhenNoPhysical(t *testing.T) {
	// 无物理网卡时，排除已知虚拟网卡，接受其余可用网卡（而非命中 docker0）
	oldPhys := isPhysicalNICBySysfs
	isPhysicalNICBySysfs = func(name string) bool { return false }
	t.Cleanup(func() { isPhysicalNICBySysfs = oldPhys })

	ifaces := []net.Interface{
		{Name: "docker0", Flags: net.FlagUp, HardwareAddr: hwAddr(t, "1e:bf:53:11:b9:f8")},
		{Name: "veth1234", Flags: net.FlagUp, HardwareAddr: hwAddr(t, "66:c0:1e:c1:55:36")},
		{Name: "ethcustom", Flags: net.FlagUp, HardwareAddr: hwAddr(t, "aa:bb:cc:dd:ee:ff")},
	}
	got := selectHardwareMAC(ifaces)
	if got != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("expected non-virtual ethcustom MAC, got %q", got)
	}
}

func TestSelectHardwareMAC_SkipsLoopbackAndDown(t *testing.T) {
	ifaces := []net.Interface{
		{Name: "lo", Flags: net.FlagUp | net.FlagLoopback, HardwareAddr: nil},
		{Name: "ens18", Flags: 0, HardwareAddr: hwAddr(t, "fe:fc:fe:a9:ca:14")}, // down
	}
	if got := selectHardwareMAC(ifaces); got != "" {
		t.Fatalf("expected empty (no usable), got %q", got)
	}
}

// #7: br0（无横杠的 Linux bridge）必须被虚拟黑名单排除。
// 当唯一可用网卡是 br0 时，其不稳定的 MAC 不应被选中。
func TestSelectHardwareMAC_BlacklistsBr0(t *testing.T) {
	ifaces := []net.Interface{
		{Name: "lo", Flags: net.FlagUp | net.FlagLoopback, HardwareAddr: nil},
		{Name: "br0", Flags: net.FlagUp, HardwareAddr: hwAddr(t, "aa:bb:cc:dd:ee:00")}, // libvirt/KVM bridge，唯一可用
	}
	got := selectHardwareMAC(ifaces)
	if got == "aa:bb:cc:dd:ee:00" {
		t.Fatalf("#7 br0 not blacklisted: got %q, expected empty", got)
	}
}

// #2: 容器内 eth0 是 veth 对端（MAC 随机），不应在第一轮被当作物理网卡选中。
// 用注入的 sysfs 判定模拟：ensphysical 是真物理，eth0 不是。
func TestSelectHardwareMAC_ContainerEth0NotPhysical(t *testing.T) {
	oldPhys := isPhysicalNICBySysfs
	isPhysicalNICBySysfs = func(name string) bool { return name == "ensphysical" }
	t.Cleanup(func() { isPhysicalNICBySysfs = oldPhys })

	ifaces := []net.Interface{
		{Name: "eth0", Flags: net.FlagUp, HardwareAddr: hwAddr(t, "02:42:ac:11:00:02")}, // Docker veth 对端
		{Name: "ensphysical", Flags: net.FlagUp, HardwareAddr: hwAddr(t, "fe:fc:fe:a9:ca:14")},
	}
	got := selectHardwareMAC(ifaces)
	// 应第一轮选中真物理 ensphysical，而非把 eth0 当物理
	if got != "fe:fc:fe:a9:ca:14" {
		t.Fatalf("#2 eth0 wrongly preferred over real physical: got %q", got)
	}
}

// #2 补充：没有任何物理网卡（sysfs 全 false）时，eth0 不在第一/二轮被选；
// 第三轮兜底接受 eth0（纯容器环境无更好来源），这是可接受的退路。
func TestSelectHardwareMAC_NoPhysicalFallbackAcceptsEth0(t *testing.T) {
	oldPhys := isPhysicalNICBySysfs
	isPhysicalNICBySysfs = func(name string) bool { return false }
	t.Cleanup(func() { isPhysicalNICBySysfs = oldPhys })

	ifaces := []net.Interface{
		{Name: "eth0", Flags: net.FlagUp, HardwareAddr: hwAddr(t, "02:42:ac:11:00:02")},
	}
	got := selectHardwareMAC(ifaces)
	if got != "02:42:ac:11:00:02" {
		t.Fatalf("expected eth0 as last-resort fallback, got %q", got)
	}
}

// withTempNodeIDDir 把 nodeIDFile 临时指向 t.TempDir()/node.id，测试后还原
func withTempNodeIDDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := nodeIDFile
	nodeIDFile = filepath.Join(dir, "node.id")
	t.Cleanup(func() { nodeIDFile = old })
	return dir
}

// C: 默认路径为 ~/.moleAgent-client/node.id
func TestDefaultNodeIDFile_UsesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := defaultNodeIDFile()
	want := filepath.Join(home, ".moleAgent-client", "node.id")
	if got != want {
		t.Fatalf("defaultNodeIDFile() = %q, want %q", got, want)
	}
}

// C: HOME 不可用时回退到可执行文件同目录（并输出 WARNING，可见而非静默）
func TestDefaultNodeIDFile_FallsBackWhenNoHome(t *testing.T) {
	t.Setenv("HOME", "") // os.UserHomeDir 在 Unix 上依赖 $HOME

	got := defaultNodeIDFile()
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable unavailable: %v", err)
	}
	want := filepath.Join(filepath.Dir(exe), "node.id")
	if got != want {
		t.Fatalf("fallback = %q, want %q", got, want)
	}
	if strings.Contains(got, ".moleAgent-client") {
		t.Fatalf("fallback must not reference the HOME-based path: %q", got)
	}
}

// C: SetNodeIDFile 重定向读写位置
func TestSetNodeIDFile_RedirectsIO(t *testing.T) {
	old := nodeIDFile
	t.Cleanup(func() { nodeIDFile = old })

	custom := filepath.Join(t.TempDir(), "custom-identity.json")
	SetNodeIDFile(custom)

	savePersistedID("mid", "ab12cd34")
	if _, err := os.Stat(custom); err != nil {
		t.Fatalf("expected file at custom path %q: %v", custom, err)
	}
	mid, nid, ok := loadPersistedID()
	if !ok || mid != "mid" || nid != "ab12cd34" {
		t.Fatalf("custom path not used for reads: (%q,%q,%v)", mid, nid, ok)
	}
}


func TestLoadSavePersistedID(t *testing.T) {
	withTempNodeIDDir(t)
	savePersistedID("machine-xyz", "ab12cd34")
	mid, nid, ok := loadPersistedID()
	if !ok || mid != "machine-xyz" || nid != "ab12cd34" {
		t.Fatalf("loadPersistedID got (%q,%q,%v), want (machine-xyz,ab12cd34,true)", mid, nid, ok)
	}
}

// #1: 原子写 —— 写完后目录中不应残留临时文件，且文件内容始终可被正确解析。
// 重复写不应产生中间截断态（os.Rename 原子替换保证）。
func TestSavePersistedID_AtomicNoTempLeftover(t *testing.T) {
	dir := withTempNodeIDDir(t)
	for i := 0; i < 5; i++ {
		savePersistedID("mid", "ab12cd34")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		// 仅允许 node.id 存在，不应有 .tmp 之类的残留
		if e.Name() != "node.id" {
			t.Fatalf("unexpected leftover file after atomic write: %q", e.Name())
		}
	}
	// 写完立即可读且合法
	if _, _, ok := loadPersistedID(); !ok {
		t.Fatal("node.id not readable immediately after atomic write")
	}
}

// #1: 写中途失败时，原 node.id 必须保持完整不被损坏（原子性核心契约）。
// 注入 writeFileAtomic 在 rename 前/中失败，验证原文件内容不变。
func TestSavePersistedID_FailurePreservesOriginal(t *testing.T) {
	dir := withTempNodeIDDir(t)
	// 先写入一个合法的原始记录
	savePersistedID("orig-mid", "ab12cd34")
	origData, err := os.ReadFile(filepath.Join(dir, "node.id"))
	if err != nil {
		t.Fatal(err)
	}

	// 注入失败的 writeFileAtomic：模拟写中途崩溃（返回错误，不真正 rename）
	oldWrite := writeFileAtomic
	writeFileAtomic = func(path string, data []byte, perm os.FileMode) error {
		return errFail // 模拟写失败
	}
	t.Cleanup(func() { writeFileAtomic = oldWrite })

	// 尝试写入新值，应失败但不损坏原文件
	savePersistedID("new-mid", "ef5678gh")

	// 原文件内容必须与失败前完全一致
	curData, err := os.ReadFile(filepath.Join(dir, "node.id"))
	if err != nil {
		t.Fatalf("original file missing after failed write: %v", err)
	}
	if string(curData) != string(origData) {
		t.Fatalf("original file corrupted by failed write:\nwant %q\ngot  %q", origData, curData)
	}
	// 原记录仍可正常读取
	mid, nid, ok := loadPersistedID()
	if !ok || mid != "orig-mid" || nid != "ab12cd34" {
		t.Fatalf("original record unreadable after failed write: (%q,%q,%v)", mid, nid, ok)
	}
}

func TestLoadPersistedID_FileMissing(t *testing.T) {
	withTempNodeIDDir(t)
	_, _, ok := loadPersistedID()
	if ok {
		t.Fatal("expected ok=false when file missing")
	}
}

func TestLoadPersistedID_CorruptFile(t *testing.T) {
	dir := withTempNodeIDDir(t)
	if err := os.WriteFile(filepath.Join(dir, "node.id"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, ok := loadPersistedID()
	if ok {
		t.Fatal("expected ok=false on corrupt file")
	}
}

// #5: 持久化的 node_id 必须通过 ValidateNodeID 校验，非法值（数字开头/非 ASCII/长度错）当无文件处理
func TestLoadPersistedID_RejectsInvalidNodeID(t *testing.T) {
	dir := withTempNodeIDDir(t)
	cases := []string{
		"12345678",  // 数字开头
		"ab12cd3",   // 长度不足 7
		"ab12cd345", // 长度超 9
		"abcé1234",  // 非 ASCII
		"",          // 空（已被现有逻辑拦截，此处确认）
	}
	for _, bad := range cases {
		writePersistedRaw(t, dir, "mid", bad)
		_, _, ok := loadPersistedID()
		if ok {
			t.Fatalf("expected ok=false for invalid node_id %q", bad)
		}
	}
}

var errFail = errFailed{}

type errFailed struct{}

func (errFailed) Error() string { return "hw failed" }

// ---- 新模型：node.id 文件是 nodeID 的真相源 ----
//
// 启动时：文件存在 → 用文件中的 node_id（锁定），machine-id 仅做漂移提示；
//         文件不存在 → 硬件指纹生成 + 落盘（无系统 machine-id 则生成伪 machine-id）。
// -id 参数优先级最高，由调用方（New）在进入文件逻辑前短路。

// 文件存在 → 锁定文件中的 nodeID，不落盘
func TestResolveNodeID_FileExistsLocksID(t *testing.T) {
	id, mid, persist := resolveNodeID("sys-mid", "ha0d7481", nil, "sys-mid", "ab12cd34", true)
	if id != "ab12cd34" {
		t.Fatalf("file exists must lock to file node_id, got %q", id)
	}
	if persist || mid != "" {
		t.Fatalf("file exists must not persist, got (mid=%q, persist=%v)", mid, persist)
	}
}

// 文件存在 + 系统 machine-id 漂移 → 仅提示，仍返回文件中的 nodeID
func TestResolveNodeID_FileExistsMachineIDDriftKeepsID(t *testing.T) {
	id, _, persist := resolveNodeID("new-sys-mid", "ha0d7481", nil, "old-sys-mid", "ab12cd34", true)
	if id != "ab12cd34" || persist {
		t.Fatalf("machine-id drift must only warn and keep file id, got (%q,%v)", id, persist)
	}
}

// 文件存在 + 系统无 machine-id（文件中是伪 machine-id）→ 仍用文件 nodeID
func TestResolveNodeID_FileExistsNoSystemMachineID(t *testing.T) {
	id, mid, persist := resolveNodeID("", "ha0d7481", nil, pseudoMachineIDPrefix+"abc123", "ab12cd34", true)
	if id != "ab12cd34" || persist || mid != "" {
		t.Fatalf("no system machine-id must still lock to file, got (%q,%q,%v)", id, mid, persist)
	}
}

// 无文件 → 硬件指纹生成，用系统 machine-id 落盘
func TestResolveNodeID_FirstRunPersistsWithSystemMachineID(t *testing.T) {
	id, mid, persist := resolveNodeID("sys-mid", "ha0d7481", nil, "", "", false)
	if id != "ha0d7481" || mid != "sys-mid" || !persist {
		t.Fatalf("first run got (%q,%q,%v), want (ha0d7481,sys-mid,true)", id, mid, persist)
	}
}

// 无文件 + 无系统 machine-id → 生成伪 machine-id 一起落盘（容器场景稳定性的核心）
func TestResolveNodeID_FirstRunNoMachineIDGeneratesPseudo(t *testing.T) {
	id, mid, persist := resolveNodeID("", "ha0d7481", nil, "", "", false)
	if id != "ha0d7481" || !persist {
		t.Fatalf("first run no machine-id got (%q,%q,%v)", id, mid, persist)
	}
	if !strings.HasPrefix(mid, pseudoMachineIDPrefix) {
		t.Fatalf("expected pseudo machine-id, got %q", mid)
	}
}

// 无文件 + 硬件指纹失败 → 随机 nodeID + 伪 machine-id 落盘
func TestResolveNodeID_FirstRunHardwareFailsUsesRandom(t *testing.T) {
	id, mid, persist := resolveNodeID("", "", errFail, "", "", false)
	if !ValidateNodeID(id) || !persist || mid == "" {
		t.Fatalf("first run hw fail got (%q,%q,%v)", id, mid, persist)
	}
}

// 伪 machine-id 两次生成应不同且带前缀标识
func TestGeneratePseudoMachineID_Unique(t *testing.T) {
	a, b := generatePseudoMachineID(), generatePseudoMachineID()
	if a == b {
		t.Fatalf("pseudo machine-id should differ: %q == %q", a, b)
	}
	if !strings.HasPrefix(a, pseudoMachineIDPrefix) {
		t.Fatalf("pseudo machine-id missing prefix: %q", a)
	}
}

// injectProbes 注入 machine-id 与硬件指纹探测，隔离真实环境
func injectProbes(t *testing.T, machineID, hwNodeID string, hwErr error) {
	t.Helper()
	oldMID, oldHW := readMachineIDFn, hardwareNodeIDFn
	readMachineIDFn = func() string { return machineID }
	hardwareNodeIDFn = func(string) (string, error) { return hwNodeID, hwErr }
	t.Cleanup(func() { readMachineIDFn = oldMID; hardwareNodeIDFn = oldHW })
}

// 端到端：首次落盘后，重启时即使硬件指纹变化（MAC 漂移）也用文件中的 nodeID
func TestEnsureNodeIDPersisted_LocksAcrossRestart(t *testing.T) {
	withTempNodeIDDir(t)

	injectProbes(t, "sys-mid", "ha0d7481", nil)
	id1 := EnsureNodeIDPersisted()
	if id1 != "ha0d7481" {
		t.Fatalf("first run got %q, want ha0d7481", id1)
	}

	// 重启：硬件指纹变了，仍应返回文件中的 ID
	injectProbes(t, "sys-mid", "fa765a31", nil)
	id2 := EnsureNodeIDPersisted()
	if id2 != "ha0d7481" {
		t.Fatalf("restart must keep persisted id, got %q", id2)
	}
}

// D 修复核心：无 machine-id 的容器每次重启 MAC 变化，nodeID 仍须稳定
func TestEnsureNodeIDPersisted_NoMachineIDStableAcrossRestart(t *testing.T) {
	withTempNodeIDDir(t)

	injectProbes(t, "", "ha0d7481", nil)
	id1 := EnsureNodeIDPersisted()

	// 容器重启：新 MAC → 新 hwNodeID；仍无 machine-id
	injectProbes(t, "", "zz111111", nil)
	id2 := EnsureNodeIDPersisted()

	if id1 != id2 {
		t.Fatalf("no-machine-id container not stable across restart: %q then %q", id1, id2)
	}
	mid, _, ok := loadPersistedID()
	if !ok || !strings.HasPrefix(mid, pseudoMachineIDPrefix) {
		t.Fatalf("expected persisted pseudo machine-id, got %q (ok=%v)", mid, ok)
	}
}

// 无 machine-id 首次落盘后，系统后来出现 machine-id → 仍以文件为准（仅提示漂移）
func TestEnsureNodeIDPersisted_SystemMachineIDAppearsLater(t *testing.T) {
	withTempNodeIDDir(t)

	injectProbes(t, "", "ha0d7481", nil)
	id1 := EnsureNodeIDPersisted()

	injectProbes(t, "real-mid-now", "ha0d7481", nil)
	id2 := EnsureNodeIDPersisted()
	if id1 != id2 {
		t.Fatalf("must keep file value when system machine-id appears, got %q then %q", id1, id2)
	}
}

// #6: DefaultNodeID 是纯计算，不应有写文件副作用（验证路径如 validateDesktopNode 会调用它）。
func TestDefaultNodeID_NoWriteSideEffect(t *testing.T) {
	dir := withTempNodeIDDir(t)
	injectProbes(t, "sys-mid", "ha0d7481", nil)

	_ = DefaultNodeID()
	if _, err := os.Stat(filepath.Join(dir, "node.id")); err == nil {
		t.Fatal("#6 DefaultNodeID wrote node.id, but it must be side-effect-free (validation paths call it)")
	}
}

// DefaultNodeID 文件存在时应返回文件中的 nodeID（只读）
func TestDefaultNodeID_ReadsFileWhenPresent(t *testing.T) {
	dir := withTempNodeIDDir(t)
	writePersistedRaw(t, dir, "sys-mid", "ab12cd34")
	injectProbes(t, "sys-mid", "ha0d7481", nil)

	if got := DefaultNodeID(); got != "ab12cd34" {
		t.Fatalf("DefaultNodeID should read file, got %q want ab12cd34", got)
	}
}

// A: ValidateNodeID 必须只接受 ASCII（逐字节），非 ASCII 多字节字符不得因数
// 长度恰好为 8 字节而混过校验。
func TestValidateNodeID_ASCIIOnly(t *testing.T) {
	bad := []string{
		"ébcdefg", // é 占 2 字节 + 6 ASCII = 8 字节
		"abééfg",  // 2 + 2 + 2 + 2 = 8 字节
		"ａbcdef",  // 全角 ａ 占 3 字节 + 5 ASCII = 8 字节
		"ab́cdef", // 组合字符
	}
	for _, id := range bad {
		if len(id) != 8 {
			t.Fatalf("test case %q is %d bytes, need exactly 8", id, len(id))
		}
		if ValidateNodeID(id) {
			t.Fatalf("ValidateNodeID(%q) = true, want false (non-ASCII must be rejected)", id)
		}
	}

	good := []string{"abcdefgh", "ha0d7481", "AbCdEf12", "a2345678"}
	for _, id := range good {
		if !ValidateNodeID(id) {
			t.Fatalf("ValidateNodeID(%q) = false, want true", id)
		}
	}
}

// 文件存在时不应做硬件探测（CPU/网卡枚举）——此时 hwNodeID 不参与决策，只读 machine-id 做提示。
func TestCurrentNodeID_FileExistsSkipsHardwareProbe(t *testing.T) {
	dir := withTempNodeIDDir(t)
	writePersistedRaw(t, dir, "sys-mid", "ab12cd34")

	oldMID, oldHW := readMachineIDFn, hardwareNodeIDFn
	readMachineIDFn = func() string { return "sys-mid" }
	hardwareNodeIDFn = func(string) (string, error) {
		t.Fatal("hardware probe must not run when node.id exists")
		return "", nil
	}
	t.Cleanup(func() { readMachineIDFn = oldMID; hardwareNodeIDFn = oldHW })

	if got := DefaultNodeID(); got != "ab12cd34" {
		t.Fatalf("DefaultNodeID got %q, want ab12cd34", got)
	}
	if got := EnsureNodeIDPersisted(); got != "ab12cd34" {
		t.Fatalf("EnsureNodeIDPersisted got %q, want ab12cd34", got)
	}
}



// writePersistedRaw 直接写一个持久化 JSON 文件（绕过 savePersistedID，用于注入篡改场景）
func writePersistedRaw(t *testing.T, dir, machineID, nodeID string) {
	t.Helper()
	data, err := json.Marshal(persistedID{MachineID: machineID, NodeID: nodeID})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node.id"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
