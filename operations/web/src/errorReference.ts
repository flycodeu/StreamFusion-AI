/** Operator guidance for concrete errors emitted by the controller/executor.
 * Keep codes exact: an unknown code must not acquire a guessed diagnosis. */
export interface ErrorReferenceEntry {
  code: string;
  name: string;
  category: string;
  meaning: string;
  steps: string[];
}

export const errorReference: ErrorReferenceEntry[] = [
  {
    code: "PORT_OBSERVATION_DENIED", name: "监听端口读取受限", category: "端口与防火墙",
    meaning: "当前执行器收到 ss 查询的明确权限拒绝。旧版曾将其他查询失败合并为此码，需结合组件版本和查询结果核对；不能认定是 sudo 权限问题。",
    steps: ["在出错主机执行 command -v ss 和 /usr/bin/ss -H -lntup，保留退出结果与错误输出。", "检查 readlink -f /usr/bin/ss 的实际路径；若终端查询正常，记录页面时间、主机和 traceId，核对该次执行器日志。", "返回端口管理重新检测；如接入检查另有授权错误，只修复该受管执行器的授权。"],
  },
  { code: "PORT_OBSERVATION_TIMEOUT", name: "监听端口查询超时", category: "端口与防火墙", meaning: "端口查询达到本次检查期限或被取消，结果尚未确认。", steps: ["在出错主机执行 /usr/bin/ss -H -lntup，核对响应时间和负载。", "主机恢复后重新检测；若仍超时，保留时间、耗时和 traceId。"] },
  { code: "PORT_COMMAND_UNAVAILABLE", name: "端口查询工具不可用", category: "端口与防火墙", meaning: "ss 工具缺失、不可执行，或实际路径未通过执行器的受保护路径检查。", steps: ["执行 command -v ss 与 readlink -f /usr/bin/ss，核对工具是否存在及实际位置。", "由管理员核对工具和父目录属主、权限及软件包来源，不要直接放宽整个目录或 sudo 权限。"] },
  { code: "PORT_OBSERVATION_FAILED", name: "监听端口查询失败", category: "端口与防火墙", meaning: "ss 查询失败，但没有足够证据归类为权限、超时或工具缺失。", steps: ["在出错主机执行 /usr/bin/ss -H -lntup，记录退出状态和原始错误。", "终端查询正常时，保留执行器版本和 traceId，由维护者核对命令调用链，再重新检测。"] },
  { code: "PORT_SNAPSHOT_TOO_LARGE", name: "端口结果超出展示上限", category: "端口与防火墙", meaning: "监听端口输出达到 64 KiB 或快照达到 256 条，平台未把截断结果当作完整状态。", steps: ["使用 ss -H -lntup 核对实际监听数量。", "结合具体服务筛选端口并保留 traceId，由管理员核对平台读取上限。"] },
  { code: "FIREWALL_EXECUTOR_REQUIRED", name: "防火墙读取需要执行器", category: "端口与防火墙", meaning: "当前主机尚不能通过受管执行器读取防火墙，已有监听端口信息不代表规则已确认。", steps: ["进入该主机的安装准备，检查接入状态。", "完成页面提示的组件准备后重新检测端口。"] },
  { code: "UFW_NOT_INSTALLED", name: "主机未安装 UFW", category: "端口与防火墙", meaning: "目标系统没有可用的 UFW；平台不能据此确认防火墙规则。", steps: ["核对主机实际使用 UFW、其他防火墙还是云安全组。", "先由管理员确定使用的防火墙，再重新检测；不要同时启用多套规则管理器。"] },
  {
    code: "OFFICIAL_PACKAGE_STATE_UNAVAILABLE", name: "系统软件包状态未确认", category: "官方安装",
    meaning: "平台没有取得完整、可信的软件包清单。查询失败、输出不完整或软件包处于未完成状态都可能导致此结果，不代表一定需要修复包事务。",
    steps: ["在目标主机执行 dpkg --audit，核对是否实际存在未完成的软件包事务；无输出通常表示未发现此类问题。", "执行 dpkg-query -W，检查命令是否成功及错误输出；若查询正常，保留 traceId，核对执行器读取结果和日志。", "仅在审计证实异常后由管理员处理对应包事务，再返回安装页面重新预检。"],
  },
  { code: "OFFICIAL_TOOL_UNAVAILABLE", name: "系统安装工具不可用", category: "官方安装", meaning: "apt、dpkg、systemctl 等所需工具缺失，或实际路径未通过受保护路径检查。", steps: ["查看预检中标出的工具路径，用 command -v 和 readlink -f 核对。", "由管理员检查该工具及其父目录的属主、权限和系统包来源，再重新预检。"] },
  { code: "OFFICIAL_PLATFORM_UNSUPPORTED", name: "系统不符合官方安装条件", category: "官方安装", meaning: "当前官方系统包配方要求 Ubuntu 24.04、amd64 架构及受控 root 执行器。", steps: ["执行 cat /etc/os-release 与 uname -m，核对系统版本和架构。", "若系统符合条件，检查主机接入状态；其他系统请选择明确支持的离线配方或自行安装后登记。"] },
  {
    code: "OFFICIAL_PATH_NOT_PROTECTED", name: "安装路径保护检查未通过", category: "官方安装",
    meaning: "计划位置或其父路径未通过保护检查。旧版执行器会误拒绝系统标准的 /lib → /usr/lib 链接；此码本身不能证明属主或权限配置错误。",
    steps: ["先记录预检指出的具体路径与 traceId，使用 namei -l 核对该路径的各级目录、属主、权限和链接目标。", "涉及 /lib 时，执行 ls -ld /lib /usr /usr/lib 和 readlink /lib。若 /lib 由 root 所有且链接目标精确为 usr/lib 或 /usr/lib，更新接入组件后重新预检；实际目标目录仍须通过保护检查。", "其他路径失败时，保留上述只读结果交由管理员核对路径归属及系统布局。"],
  },
  {
    code: "OFFICIAL_PACKAGE_ALREADY_INSTALLED", name: "系统已有目标软件包", category: "官方安装",
    meaning: "配方需要的至少一个软件包已经安装，平台停止覆盖安装；这不代表整套服务已经安装完成或正在运行。",
    steps: ["对预检列出的具体包使用 dpkg-query -W 查询版本；结合 dpkg --audit 核对软件包状态。", "核对页面列出的服务单元及现有配置、数据路径。已有可用软件请选择自行安装后的检测登记；只有部分包时，先由管理员确认现有安装用途。"],
  },
  {
    code: "OFFICIAL_EXISTING_CONFIGURATION_OR_DATA", name: "目标位置已有内容或状态未确认", category: "官方安装",
    meaning: "计划使用的程序、服务单元、配置或数据位置已存在，或平台无法确认该位置不存在；这与软件包是否已经安装是两项独立检查。",
    steps: ["按预检列出的具体路径使用 ls -ld 和 namei -l，只读核对内容是否存在、路径归属及查询错误。服务单元和其覆盖目录也在检查范围内。", "确认是已有服务时选择检测登记；目录残留或查询失败时，保留具体路径、错误输出和 traceId，由管理员核对来源与用途。"],
  },
  { code: "OFFICIAL_PORT_UNAVAILABLE", name: "官方默认端口不可用", category: "官方安装", meaning: "配方默认端口不能使用，可能已被现有服务占用。", steps: ["进入端口管理检测该主机，查找预检列出的端口和进程。", "确认现有服务用途并处理冲突后重新预检。"] },
  { code: "OFFICIAL_APT_PLAN_UNAVAILABLE", name: "APT 安装模拟失败", category: "官方安装", meaning: "APT 无法生成安装计划；本机索引、依赖约束或软件源状态需要进一步检查。", steps: ["执行 apt-cache policy，检查当前索引和候选来源。", "对计划列出的包运行 apt-get --simulate --no-remove --no-install-recommends install <包名>，查看具体依赖错误；此处只做模拟。", "处理已确认的软件源或依赖问题后重新预检。"] },
  { code: "OFFICIAL_PACKAGE_ORIGIN_UNAVAILABLE", name: "软件包来源未确认", category: "官方安装", meaning: "读取 apt-cache policy 失败，或候选版本与软件源索引无法对应。", steps: ["执行 apt-cache policy <包名> 和 apt-cache policy，核对预检列出的版本、来源和索引。", "检查软件源是否可用；保留查询输出和 traceId 后重新预检。"] },
  { code: "OFFICIAL_PACKAGE_ORIGIN_UNTRUSTED", name: "软件包来源不符合配方", category: "官方安装", meaning: "候选包没有通过配方要求的系统仓库来源校验。", steps: ["查看 apt-cache policy <包名>，核对实际仓库、发行版和候选版本。", "确认软件源及签名配置，不要关闭签名验证来绕过检查；若使用系统官方镜像，提供 policy 输出和 traceId 排查。"] },
  { code: "OFFICIAL_PACKAGE_PLAN_REMOVES_SOFTWARE", name: "安装计划会删除已有软件", category: "官方安装", meaning: "APT 模拟包含删除或清除已有包，超出了只新增软件的安装范围。", steps: ["查看模拟计划中 Remv 或 Purg 对应的软件。", "由管理员先处理已确认的依赖冲突；重新预检前不要强行删除其他软件。"] },
  { code: "OFFICIAL_PACKAGE_PLAN_UPGRADES_OR_UNTRUSTED", name: "安装计划包含升级或无法识别的包", category: "官方安装", meaning: "模拟计划涉及已有软件升级，或候选包输出不符合受信配方格式。", steps: ["检查 APT 模拟输出中的 Inst 行与当前已安装版本。", "保留完整模拟输出和 traceId，核对冲突依赖或配方支持范围，再重新预检。"] },
  { code: "OFFICIAL_PACKAGE_PLAN_INCOMPLETE", name: "安装计划缺少必要软件包", category: "官方安装", meaning: "模拟结果没有包含配方要求的全部主软件包。", steps: ["核对 apt-cache policy 中每个主软件包的候选版本。", "检查本机索引、软件包已安装状态及依赖模拟输出后重新预检。"] },
  { code: "OFFICIAL_PACKAGE_PLAN_TOO_LARGE", name: "安装计划超过读取上限", category: "官方安装", meaning: "APT 模拟输出或包数量超出平台的有界读取范围。", steps: ["保留模拟输出、计划包名和 traceId，核对是否存在异常依赖链。", "请维护者检查读取上限；不要把截断输出当成可执行计划。"] },
  { code: "OFFICIAL_PACKAGE_POLICY_TOO_LARGE", name: "软件源信息超过读取上限", category: "官方安装", meaning: "软件源 policy 输出超过平台的有界读取范围，来源校验未完成。", steps: ["分别查看 apt-cache policy 与 apt-cache policy <包名> 的输出量。", "保留 traceId 和相关软件源信息，请维护者核对读取范围。"] },
  { code: "OFFICIAL_PLAN_CHANGED", name: "确认后的安装计划已变化", category: "官方安装", meaning: "软件包版本、依赖或来源与之前确认的计划不一致，平台停止继续安装。", steps: ["重新预检并比较软件包版本、依赖和来源。", "确认新计划后再提交；不要直接重复发送旧安装命令。"] },
  { code: "OFFICIAL_PACKAGE_RECONCILE_REQUIRED", name: "软件包安装结果待核对", category: "官方安装", meaning: "包安装命令已执行，但结果不能确认为成功，原作业需要核对。", steps: ["打开原作业的日志和检查结果，记录 jobId 与 traceId。", "使用 dpkg --audit、dpkg-query -W 和对应服务状态核对目标现状。", "通过原作业的检查结果入口继续确认，避免重新创建安装作业。"] },
  { code: "OFFICIAL_PACKAGE_VERSION_MISMATCH", name: "实际软件包版本与计划不符", category: "官方安装", meaning: "目标已安装的精确软件包版本与冻结计划不同。", steps: ["对照原计划和 dpkg-query -W 查询结果。", "核对期间是否有其他包升级操作，再检查原作业结果。"] },
  { code: "OFFICIAL_PACKAGE_CONTENT_DRIFT", name: "软件包文件发生变化", category: "官方安装", meaning: "实际软件包文件没有通过安装后的完整性核验。", steps: ["查看原作业和 dpkg -V <包名> 的检查结果。", "核对文件是否被人工修改、替换或丢失，不要直接覆盖已有配置和数据。"] },
  { code: "DOCKER_PACKAGE_STATE_UNAVAILABLE", name: "Docker 相关包状态未确认", category: "Docker", meaning: "平台未能完整确认系统软件包状态，不能据此判定 Docker 尚未安装。", steps: ["执行 dpkg --audit 和 dpkg-query -W，区分包事务异常与清单读取失败。", "保留时间和 traceId，查看安装预检与执行器日志。"] },
  { code: "DOCKER_PACKAGE_TRANSACTION_PENDING", name: "系统软件包事务未完成", category: "Docker", meaning: "包清单中发现未完成的安装或配置状态，尚不能继续安装。", steps: ["执行 dpkg --audit，定位具体软件包和未完成步骤。", "让服务器管理员处理已确认的包事务后重新预检；不要清空包数据库或删除现有软件来绕过检查。"] },
  { code: "DOCKER_NOT_READY", name: "Docker 引擎不可用", category: "Docker", meaning: "执行器无法访问 Docker 引擎；仅有 Docker 软件登记不表示引擎已安装并运行。", steps: ["在软件管理的安装入口确认 Docker 是否已安装。", "已安装时查看 systemctl status docker docker.socket 及原作业日志，核对服务状态和 socket。"] },
  { code: "EXECUTOR_UPGRADE_REQUIRED", name: "目标组件能力需要更新", category: "主机接入", meaning: "所选操作要求的协议或能力版本与目标执行器不兼容；已经接入不代表具备所有新增能力。", steps: ["打开该主机的安装准备，核对提示的具体能力并更新接入组件。", "更新成功后重新预检原操作，不需要重新创建主机记录。"] },
  { code: "SUDO_AUTHORIZATION_REQUIRED", name: "受管执行器授权缺失", category: "主机接入", meaning: "执行器调用没有通过当前受限授权检查。", steps: ["先检查该主机的接入诊断，核对 SSH 登录账号和受管目录。", "使用页面提供的授权修复流程，仅修复指定执行器入口；密码含义以当前 SSH 账号的 sudo 配置为准。"] },
  { code: "OPERATION_LOCKED", name: "主机存在未决操作", category: "作业", meaning: "原作业尚未结束或结果待确认，平台保留锁以防止重复变更。", steps: ["打开该主机的操作记录，找到排队、运行中或 UNKNOWN 作业。", "尚未执行的排队作业可取消；其余通过原作业检查结果，不能直接删除锁。"] },
  { code: "TARGET_RESULT_UNKNOWN", name: "目标操作结果未确认", category: "作业", meaning: "请求后未取得可验证的最终回执，不能判断目标是否已经完成变更。", steps: ["恢复主机连接并查看原作业日志。", "使用原作业的检查结果入口，不要重复安装或卸载。"] },
];

// The same dpkg inventory reader is used by native Docker and official installs.
// Only its namespace differs; keep the diagnosis and read-only checks identical.
for (const [prefix, category] of [["OFFICIAL", "官方安装"], ["DOCKER", "Docker"]] as const) {
  errorReference.push(
    { code: `${prefix}_PACKAGE_STATE_TOO_LARGE`, name: "软件包清单超过读取上限", category, meaning: "dpkg 查询结果达到当前 4 MiB 上限，平台无法确认清单完整性。这是读取范围问题，不能据此认定系统包损坏。", steps: ["执行 dpkg-query -W，核对清单是否正常返回；用 dpkg --audit 检查是否另有包事务问题。", "保留软件包数量、输出大小、执行器版本和 traceId，请维护者核对清单读取范围。"] },
    { code: `${prefix}_PACKAGE_STATE_INVALID`, name: "软件包清单格式未识别", category, meaning: "查询输出不符合包名、三位状态和版本的固定格式，不能据此确认已安装状态。", steps: ["执行 dpkg-query --show --showformat='${binary:Package}\\t${db:Status-Abbrev}\\t${Version}\\n'，检查退出结果及异常输出。", "若该查询正常，保留 traceId 与执行器版本，核对是否有输出截断或解析问题；不要先修改系统包数据库。"] },
    { code: `${prefix}_PACKAGE_STATE_TIMEOUT`, name: "软件包状态查询超时", category, meaning: "读取 dpkg 软件包清单超过本次检查期限，状态尚未确认。", steps: ["检查目标主机当前负载，并在终端执行 dpkg-query -W 核对查询耗时。", "待主机恢复后重新预检；若持续超时，保留时间、耗时与 traceId 排查。"] },
  );
}
errorReference.push({ code: "OFFICIAL_PACKAGE_TRANSACTION_PENDING", name: "系统软件包事务未完成", category: "官方安装", meaning: "包清单证实存在未完成的安装、配置或错误状态，平台停止后续安装。", steps: ["执行 dpkg --audit，定位具体软件包和未完成步骤。", "由服务器管理员处理已经确认的包事务，完成后重新预检。"] });

export function findErrorReference(code: string): ErrorReferenceEntry | undefined {
  return errorReference.find((entry) => entry.code === code.trim().toUpperCase());
}

export function searchErrorReference(query: string): ErrorReferenceEntry[] {
  const terms = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  return errorReference.filter((entry) => {
    const text = `${entry.code} ${entry.name} ${entry.category} ${entry.meaning} ${entry.steps.join(" ")}`.toLowerCase();
    return terms.every((term) => text.includes(term));
  });
}
