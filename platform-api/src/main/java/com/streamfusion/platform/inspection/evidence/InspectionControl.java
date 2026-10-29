package com.streamfusion.platform.inspection.evidence;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("inspection_control")
public class InspectionControl {
    @TableId private String controlId;
    private String planId;
    private String nodeId;
    private String targetBootId;
    private Integer controlSeq;
    private String desiredState;
    private LocalDateTime expiresAt;
}
