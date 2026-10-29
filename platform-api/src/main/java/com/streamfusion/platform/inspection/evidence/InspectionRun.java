package com.streamfusion.platform.inspection.evidence;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("inspection_run")
public class InspectionRun {
    @TableId private String runId;
    private String jobId;
    private Integer assignmentEpoch;
    private String controlId;
    private Integer controlSeq;
    private String nodeId;
    private String bootId;
    private String specJson;
    private String state;
    private LocalDateTime leaseExpiresAt;
    private Boolean resourcesReleased;
}
