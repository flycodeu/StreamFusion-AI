package com.streamfusion.platform.audit.pojo.entity;

import com.baomidou.mybatisplus.annotation.IdType;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import io.swagger.v3.oas.annotations.media.Schema;
import java.time.LocalDateTime;
import lombok.AllArgsConstructor;
import lombok.Data;
import lombok.NoArgsConstructor;

/** 安全操作审计的持久化数据。 */
@Data
@NoArgsConstructor
@AllArgsConstructor
@TableName("sys_operation_log")
@Schema(description = "操作审计数据", hidden = true)
public class OperationLogEntity {
    /** 后端生成的审计记录主键。 */
    @TableId(type = IdType.ASSIGN_ID)
    @Schema(description = "审计记录ID")
    private Long id;

    /** 操作者ID。 */
    @Schema(description = "操作者ID")
    private Long actorId;

    /** 操作目标类型。 */
    @Schema(description = "目标类型")
    private String targetType;

    /** 目标ID。 */
    @Schema(description = "目标ID")
    private Long targetId;

    /** 审计动作编码。 */
    @Schema(description = "操作编码")
    private String action;

    /** 操作结果：SUCCESS 成功，FAILURE 失败，DENIED 拒绝。 */
    @Schema(description = "操作结果")
    private String result;

    /** 失败或拒绝原因编码，不保存敏感原文。 */
    @Schema(description = "原因编码")
    private String reasonCode;

    /** 业务调用方显式选择的对象与关联摘要，JSON格式。 */
    @Schema(description = "对象与关联摘要")
    private String changes;

    /** 请求跟踪标识，用于关联服务日志。 */
    @Schema(description = "跟踪ID")
    private String traceId;

    /** 审计记录时间，使用北京时间。 */
    @Schema(description = "操作时间")
    private LocalDateTime createdAt;

    /** 连接来源 IP，不直接信任转发请求头。 */
    @Schema(description = "来源IP")
    private String sourceIp;

    /** 清理控制字符并限制长度的客户端摘要。 */
    @Schema(description = "客户端摘要")
    private String clientSummary;
}
