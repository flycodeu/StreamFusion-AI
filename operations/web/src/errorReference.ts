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
  {code:"RUNTIME_POLICY_READ_FAILED",name:"运行策略读取失败",category:"软件运行",meaning:"暂时无法确认目标服务的实际运行策略。",steps:["在软件详情的“配置与运行”中重新读取，检查服务单元或容器是否存在及读取权限。"]},
  {code:"RUNTIME_POLICY_DOCKER_BOOT_DISABLED",name:"Docker 未开启开机启动",category:"软件运行",meaning:"容器选择了开机恢复，但主机 Docker Engine 尚未开启开机启动。",steps:["先读取 Docker Engine 的运行策略，按主机需要设置开机启动，再处理原操作。"]},
  {code:"RUNTIME_POLICY_HELD_CONFIGURATION",name:"软件已保持停止",category:"软件运行",meaning:"当前软件处于持久停止状态，普通配置变更会重启服务，因此本次变更已停止。",steps:["先启动软件，再修改端口或目录等运行配置。运行策略本身可在停止状态下修改，下次启动时恢复。"]},
  {code:"RUNTIME_POLICY_STOP_TIMEOUT_IMMUTABLE",name:"容器停止等待时间不能直接修改",category:"软件运行",meaning:"已有容器的停止等待时间不支持通过策略更新直接改变。",steps:["重新读取运行策略并保留当前停止等待时间，调整退出恢复方式后再提交。"]},
  {code:"RUNTIME_POLICY_UNCONFIRMED",name:"运行策略结果待核对",category:"软件运行",meaning:"策略操作可能已执行，控制器尚未取得完整的确认结果。",steps:["从执行记录打开原操作，核对结果后继续；保留原任务，不重复提交新的策略操作。"]},
  {code:"RUNTIME_POLICY_RECORD_DAMAGED",name:"运行策略文件不可读",category:"软件运行",meaning:"服务器上的受管策略文件缺失、损坏或身份不符。",steps:["保留现有服务和文件，查看原操作日志，由管理员核对策略文件的归属与内容。"]},
  {code:"RUNTIME_POLICY_BINDING_CONFLICT",name:"服务身份在配置期间发生变化",category:"软件运行",meaning:"更新策略时读取到的服务身份与原作业不一致。",steps:["核对原操作日志与目标服务，确认是否有其他工具修改或替换了服务配置。"]},
  {code:"RUNTIME_POLICY_CLEANUP_UNCONFIRMED",name:"运行策略清理待核对",category:"软件运行",meaning:"软件卸载后，其受管运行策略文件尚未确认清理完成。",steps:["从执行记录核对原卸载操作，检查目标服务的受管策略文件；保留原任务及其他配置。"]},
  {code:"COMMAND_PYTHON_NOT_FOUND",name:"Python 解释器不存在",category:"软件安装",meaning:"所选 Python 路径不存在，安装尚未开始。",steps:["选择当前主机已有的兼容 Python 路径，或先安装对应版本。ytlabel 的 Python 3.12 方案可使用兼容的 /usr/bin/python3。"]},
  {code:"COMMAND_PYTHON_NOT_EXECUTABLE",name:"Python 无法执行",category:"软件安装",meaning:"所选 Python 文件不具备执行条件。",steps:["核对解释器路径与执行权限，再检查安装条件。"]},
  {code:"COMMAND_PYTHON_PATH_INVALID",name:"Python 路径不完整",category:"软件安装",meaning:"Python 参数需要完整的可执行文件路径。",steps:["填写完整路径，例如 /usr/bin/python3，而非版本号或目录。"]},
  {code:"COMMAND_PYTHON_PATH_UNSAFE",name:"Python 路径不适用",category:"软件安装",meaning:"解释器路径不符合受管服务的权限要求。",steps:["选择由管理员维护的系统或 Ops 运行目录中的解释器。"]},
  {code:"COMMAND_PYTHON_VERSION_UNSUPPORTED",name:"Python 版本不匹配",category:"软件安装",meaning:"解释器版本不符合当前安装方案的依赖要求。",steps:["核对所选方案需要的 Python 版本，选择匹配的解释器；使用 Python 3.12 时为 ytlabel 选择对应兼容方案。"]},
  {code:"COMMAND_PYTHON_CHECK_UNAVAILABLE",name:"Python 检查未完成",category:"软件安装",meaning:"暂时无法读取完整的解释器环境。",steps:["检查解释器、文件读取权限和服务器负载后重试。"]},
  {code:"COMMAND_PYTHON_MODULE_MISSING",name:"Python 组件缺失",category:"软件安装",meaning:"解释器缺少安装方案必需的标准组件。",steps:["按检查结果补齐 venv、ensurepip 等对应运行组件。"]},
  {code:"COMMAND_PYTHON_LIBRARY_MISSING",name:"系统依赖库缺失",category:"软件安装",meaning:"当前软件需要的系统动态库尚未准备。",steps:["按检查结果安装对应系统库，再重新检查安装条件。"]},
  {code:"COMMAND_PYTHON_SQLITE_UNSUPPORTED",name:"SQLite 版本不匹配",category:"软件安装",meaning:"Python 内置 SQLite 不满足所选软件的运行要求。",steps:["Label Studio 需要 SQLite 3.35 以上，请选择兼容的 Python 解释器。"]},
  {code:"COMMAND_CATALOG_KEY_INVALID",name:"所属软件标识无效",category:"软件配置",meaning:"软件配置的所属软件标识不符合格式要求。",steps:["选择已有软件，或填写以小写字母开头、仅含小写字母数字和连字符的标识，最多64个字符。"]},
  {code:"COMMAND_TRUST_REQUIRED",name:"需要安装授权",category:"软件安装",meaning:"目标服务器需要授权本次命令配置。",steps:["在安装窗口继续验证权限；需要密码时，输入所选服务器账号的 sudo 密码。"]},
  {code:"EXECUTOR_CHECK_EXPIRED",name:"检查请求已过期",category:"主机接入",meaning:"执行器拒绝了有效期不符合要求的检查请求。",steps:["重新检查安装条件；若反复出现，核对 Ops 所在机器与目标服务器的系统时间。"]},
  {code:"COMMAND_STEP_ORDER_INVALID",name:"步骤顺序不正确",category:"软件配置",meaning:"安装或卸载的结果核对必须放在最后，卸载范围核对必须放在最前。",steps:["把新增命令放在结果核对之前；卸载命令放在范围核对之后，再保存配置。"]},
  {code:"COMMAND_STEP_INVALID",name:"步骤配置不完整",category:"软件配置",meaning:"步骤名称、命令、执行权限、超时或适用方案不符合要求。",steps:["在软件配置中检查当前步骤，填写名称和命令，并选择适用的运行方式与来源。"]},
  {code:"BUILTIN_TEMPLATE_CONTRACT_CHANGED",name:"基础处理步骤被修改",category:"软件配置",meaning:"内置方案的材料或基础处理步骤与当前定义不一致。",steps:["重新打开软件配置，保留材料检查和结果核对步骤；通过新增命令或修改已有命令调整安装过程。"]},
  {code:"COMMAND_SIGNATURE_INVALID",name:"命令签名无效",category:"软件安装",meaning:"目标服务器拒绝了未经当前控制器签名或已被修改的命令。",steps:["核对主机与控制器归属，重新生成安装计划。"]},
  {code:"COMMAND_STEP_FAILED",name:"命令执行失败",category:"软件安装",meaning:"安装或卸载步骤退出失败。",steps:["在“日志记录 → 执行记录”查看失败步骤，核对服务器实际状态。"]},
  {code:"COMMAND_SCRIPT_LINE_ENDINGS_INVALID",name:"安装脚本格式有误",category:"软件安装",meaning:"安装尚未开始，脚本包含不适用的换行格式。",steps:["在“软件配置”中重新保存并发布安装方案，再重新安装。"]},
  {code:"COMMAND_STEP_INTERRUPTED",name:"命令执行中断",category:"软件安装",meaning:"步骤超时或执行被中断，部分操作可能已经完成。",steps:["先核对原任务结果，不要重复运行安装命令。"]},
  {code:"COMMAND_STEP_RECONCILE_REQUIRED",name:"命令结果待核对",category:"软件安装",meaning:"上次命令可能已执行，尚未取得可确认的结果。",steps:["在“日志记录 → 执行记录”核对原任务输出和服务器实际状态，再从“软件安装”的“查看进度”处理原任务。"]},
  {code:"COMMAND_STEP_UNCONFIRMED",name:"命令结果待核对",category:"软件安装",meaning:"执行器尚未确认此步骤是否完成。",steps:["查看原任务输出并检查软件状态，避免重复执行已完成的步骤。"]},
  {code:"COMMAND_VERIFY_FAILED",name:"安装验证未通过",category:"软件安装",meaning:"安装后的验证命令未通过。",steps:["在安装输出中查看验证命令及退出结果，核对程序路径、服务状态和配置。"]},
  {code:"INSTALL_REQUEST_TOO_LARGE",name:"命令与参数过长",category:"软件安装",meaning:"安装命令与参数合计超过允许大小。",steps:["精简安装方案中的脚本，再重新检查安装条件。"]},
  {code:"COMMAND_ACCOUNT_REQUIRED",name:"需要安装账号",category:"软件安装",meaning:"方案包含使用普通账号执行的步骤。",steps:["选择目标服务器上用于执行此步骤的账号，再重新检查安装条件。"]},
  {code:"COMMAND_ACCOUNT_INVALID",name:"安装账号不可用",category:"软件安装",meaning:"所选账号不存在或不符合命令的执行要求。",steps:["检查目标服务器上的账号及其目录权限，再重新检查安装条件。"]},
  {code:"NATIVE_DOWNLOAD_INTERRUPTED",name:"安装文件下载中断",category:"软件安装",meaning:"目标服务器下载安装文件时连接中断。",steps:["恢复网络后，从“软件安装”的“查看进度”继续原任务。"]},
  { code: "PREPARATION_UPLOAD_STALLED", name: "组件上传停滞", category: "主机接入", meaning: "上传连续 45 秒没有推进，连接已停止，安装尚未开始。", steps: ["检查 SSH 网络连通性，恢复后重试。"] },
  { code: "PREPARATION_UPLOAD_TIMEOUT", name: "组件上传超时", category: "主机接入", meaning: "接入流程达到 15 分钟上限，上传未完成，安装尚未开始；不代表权限不足。", steps: ["查看已传大小和平均速度；持续低速可使用离线接入包。", "恢复连接后重试；连续 45 秒无进度会提前停止。"] },
  { code: "PREPARATION_UPLOAD_INTERRUPTED", name: "组件上传连接中断", category: "主机接入", meaning: "SSH 连接在传输完成前中断。", steps: ["检查网络与 SSH 服务状态后重试。"] },
  { code: "PREPARATION_UPLOAD_NO_SPACE", name: "上传临时空间不足", category: "主机接入", meaning: "目标服务器明确报告临时目录空间或配额不足。", steps: ["检查目标服务器 /tmp 的可用空间及账号配额。"] },
  { code: "PREPARATION_UPLOAD_DENIED", name: "上传目录不可写", category: "主机接入", meaning: "目标服务器拒绝创建或写入上传临时目录。", steps: ["检查 SSH 账号对 /tmp 的写权限及文件系统状态。"] },
  { code: "PREPARATION_UPLOAD_FAILED", name: "组件上传未完成", category: "主机接入", meaning: "尚未获得足够信息判断具体原因，不能直接归为权限问题。", steps: ["保留 traceId，查看传输大小、速度及控制器日志。"] },
  { code: "NODE_REGISTRY_CAPABILITY_REQUIRED", name: "组件不支持 npm 镜像管理", category: "Node 环境", meaning: "当前主机组件尚不支持账号级 npm registry 管理。", steps: ["更新接入组件后重新预检。", "更新组件不会自动切换服务器镜像地址。"] },
  { code: "NODE_REGISTRY_CHANGED", name: "镜像或账号配置已变化", category: "Node 环境", meaning: "镜像列表或账号配置已变化，请重新预检并确认。", steps: ["刷新镜像地址列表并检测账号配置。", "重新选择地址并预检确认；删除列表项不会改变服务器已有配置。"] },
  { code: "NODE_TERMINAL_CAPABILITY_REQUIRED", name: "组件不支持 Node 终端接入", category: "Node 环境", meaning: "当前主机组件尚不支持为所选账号配置 Bash。", steps: ["更新接入组件后重新预检。", "更新组件不会自动执行 Node 安装或终端接入。"] },
  { code: "NODE_TERMINAL_CONFLICT", name: "Bash 接入配置冲突", category: "Node 环境", meaning: "现有启动文件中的接入片段与本次环境不匹配，操作已停止。", steps: ["检查该账号启动文件中的 Ops 片段及对应环境。", "保留原配置，处理冲突后重新预检，不要覆盖整个启动文件。"] },
  { code: "NODE_TERMINAL_CHANGED", name: "Bash 配置已变化", category: "Node 环境", meaning: "启动文件在预检后发生变化，本次操作已停止。", steps: ["核对是否有其他管理员修改了账号启动文件。", "重新预检并确认新的变更范围。"] },
  { code: "NODE_TERMINAL_RESULT_UNCONFIRMED", name: "终端接入结果未确认", category: "Node 环境", meaning: "尚未确认 Bash 接入片段的最终状态。", steps: ["在原作业检查执行结果，不要重复提交。", "核对后重新检测环境；新配置仅对后续登录生效。"] },
  { code: "EXECUTOR_DOCKER_ACCESS_UNSUPPORTED", name: "组件不支持 Docker 账号权限", category: "Docker", meaning: "已接入组件不具备本次账号权限检查和变更能力。", steps: ["在账号权限页更新接入组件，完成后重新检查。", "核对账号与当前组成员状态，再明确确认授权；组件更新不会自动授权。"] },
  { code: "DOCKER_ACCESS_STATE_CHANGED", name: "Docker 账号状态已变化", category: "Docker", meaning: "账号、Docker 组或引擎身份与确认时不同，本次变更未受理。", steps: ["重新检查权限，核对所选账号、组成员与引擎状态。", "按新检查结果重新确认操作，不要覆盖其他管理员的变更。"] },
  { code: "DOCKER_ACCESS_SOCKET_NOT_READY", name: "Docker 连接入口未就绪", category: "Docker", meaning: "标准 Docker socket 未通过属主、组和权限检查，不能授予新权限。", steps: ["检查 Docker Engine 状态以及 /run/docker.sock 是否为 root:docker、0660。", "由管理员核对原服务配置，不要改为 0666。收回已有补充组权限不要求 socket 存在。"] },
  { code: "DOCKER_ACCESS_PRIMARY_GROUP", name: "docker 是账号主组", category: "Docker", meaning: "本功能仅管理 docker 补充组，不能更改账号主组。", steps: ["核对账号的主组和其他服务依赖。", "如需调整主组，请由服务器管理员单独处理，再返回检查。"] },
  { code: "DOCKER_ACCESS_ACCOUNT_INELIGIBLE", name: "账号不可分配权限", category: "Docker", meaning: "所选账号不符合现有本地普通登录账号条件。", steps: ["选择列表中可分配的普通账号。", "核对账号 UID 和登录 shell；本功能不创建账号或启用被禁止登录的账号。"] },
  { code: "DOCKER_ACCESS_ALREADY_SET", name: "账号权限无需变更", category: "Docker", meaning: "账号已经处于所选 docker 组成员状态。", steps: ["重新检查权限，无需重复授权或收回。", "刚授权后需重新登录；现有会话不会立即获得新组成员身份。"] },
  { code: "SOFTWARE_IDENTITY_DRIFT", name: "软件身份已变化", category: "软件管理", meaning: "当前程序或服务与 Ops 登记的身份不一致，无法安全读取该实例日志或继续操作。", steps: ["核对这台主机是否通过平台外方式替换、升级或重建了该软件。", "在软件详情重新检查状态并核对实际程序路径；保留原作业和数据，不要重复安装覆盖。"] },
  { code: "SOFTWARE_OWNERSHIP_MISMATCH", name: "软件归属不匹配", category: "软件管理", meaning: "目标上的软件归属记录与当前主机或实例不匹配，操作已停止。", steps: ["核对主机、安装目录与软件实例，检查是否复制或移动过受管目录。", "查看原安装作业和诊断信息，由管理员核对归属后再操作；不要手动修改归属记录绕过检查。"] },
  { code: "SOFTWARE_LOG_READ_DENIED", name: "软件日志不可读取", category: "软件管理", meaning: "执行器未能通过受支持的日志入口读取该软件日志；不能仅凭此码认定是 sudo 密码问题。", steps: ["检查软件详情中的运行方式和服务标识，核对 systemd 或 Docker 服务是否存在、可访问。", "记录发生时间、主机和 traceId，检查对应服务日志读取权限及目标执行器日志。"] },
  { code: "PREPARATION_UPDATE_UNKNOWN_BINARY", name: "组件来源未确认", category: "主机接入", meaning: "服务器组件摘要没有匹配的成功接入记录，已停止自动更新。", steps: ["核对当前主机、接入目录及原安装来源。", "在主机安装准备中核对后手动更新，再继续预检。"] },
  { code: "PREPARATION_UPDATE_CHANGED", name: "组件状态已变化", category: "主机接入", meaning: "检查后组件或更新资格发生变化，本次自动更新停止。", steps: ["查看该主机是否有其他操作或人工更新。", "待原操作完成后重新预检，未知结果先在原作业核对。"] },
  { code: "PREPARATION_UPDATE_ONLY", name: "自动更新条件不满足", category: "主机接入", meaning: "接入目录、身份或摘要发生变化，自动更新不会转为首次安装或权限修复。", steps: ["查看原更新作业的结果与错误详情。", "在主机安装准备中核对目录和身份后处理，保留原作业记录。"] },
  { code: "PREPARATION_SUDO_DENIED", name: "账号无组件更新权限", category: "主机接入", meaning: "当前 SSH 账号不具备本次提权权限，输入密码不会增加权限。", steps: ["核对主机配置的 SSH 账号及 sudo 拒绝原因。", "请管理员确认组件维护权限后再继续，不要扩大已有软件操作授权。"] },
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
  { code: "OFFICIAL_PACKAGE_RECONCILE_REQUIRED", name: "软件包安装结果待核对", category: "官方安装", meaning: "包安装命令已执行，但结果不能确认为成功，原作业需要核对。", steps: ["在“日志记录 → 执行记录”打开原作业，展开诊断信息。", "使用 dpkg --audit、dpkg-query -W 和对应服务状态核对目标现状。", "返回“软件安装”的“查看进度”，核对原作业结果，避免重新创建安装作业。"] },
  { code: "OFFICIAL_PACKAGE_VERSION_MISMATCH", name: "实际软件包版本与计划不符", category: "官方安装", meaning: "目标已安装的精确软件包版本与冻结计划不同。", steps: ["对照原计划和 dpkg-query -W 查询结果。", "核对期间是否有其他包升级操作，再检查原作业结果。"] },
  { code: "OFFICIAL_PACKAGE_CONTENT_DRIFT", name: "软件包文件发生变化", category: "官方安装", meaning: "实际软件包文件没有通过安装后的完整性核验。", steps: ["查看原作业和 dpkg -V <包名> 的检查结果。", "核对文件是否被人工修改、替换或丢失，不要直接覆盖已有配置和数据。"] },
  { code: "DOCKER_PACKAGE_STATE_UNAVAILABLE", name: "Docker 相关包状态未确认", category: "Docker", meaning: "平台未能完整确认系统软件包状态，不能据此判定 Docker 尚未安装。", steps: ["执行 dpkg --audit 和 dpkg-query -W，区分包事务异常与清单读取失败。", "保留时间和 traceId，查看安装预检与执行器日志。"] },
  { code: "DOCKER_PACKAGE_TRANSACTION_PENDING", name: "系统软件包事务未完成", category: "Docker", meaning: "包清单中发现未完成的安装或配置状态，尚不能继续安装。", steps: ["执行 dpkg --audit，定位具体软件包和未完成步骤。", "让服务器管理员处理已确认的包事务后重新预检；不要清空包数据库或删除现有软件来绕过检查。"] },
  { code: "DOCKER_NOT_READY", name: "Docker 引擎不可用", category: "Docker", meaning: "执行器无法访问 Docker 引擎；仅有 Docker 软件登记不表示引擎已安装并运行。", steps: ["在“软件安装”页面选择主机，检查 Docker Engine 状态。", "已安装时查看 systemctl status docker docker.socket 及原作业日志，核对服务状态和 socket。"] },
  { code: "EXECUTOR_UPGRADE_REQUIRED", name: "目标组件能力需要更新", category: "主机接入", meaning: "所选操作要求的协议或能力版本与目标执行器不兼容；已经接入不代表具备所有新增能力。", steps: ["打开该主机的安装准备，核对提示的具体能力并更新接入组件。", "更新成功后重新预检原操作，不需要重新创建主机记录。"] },
  { code: "SUDO_AUTHORIZATION_REQUIRED", name: "受管执行器授权缺失", category: "主机接入", meaning: "执行器调用没有通过当前受限授权检查。", steps: ["先检查该主机的接入诊断，核对 SSH 登录账号和受管目录。", "使用页面提供的授权修复流程，仅修复指定执行器入口；密码含义以当前 SSH 账号的 sudo 配置为准。"] },
  { code: "OPERATION_LOCKED", name: "主机存在未决操作", category: "作业", meaning: "原作业尚未结束或结果待确认，平台保留锁以防止重复变更。", steps: ["在相关功能页打开原任务，查看排队、运行中或结果待核对的操作。", "尚未执行的排队作业可取消；其余通过原作业检查结果，不能直接删除锁。"] },
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
