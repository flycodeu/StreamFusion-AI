package com.streamfusion.platform.audit.pojo.dto;

import com.streamfusion.platform.audit.pojo.entity.OperationLogEntity;
import lombok.Getter;
import lombok.Setter;

/** Read projection preserves records when their actor account has been deleted. */
@Getter
@Setter
public class AuditRow extends OperationLogEntity {
    private String username;
    private String nickname;
}
