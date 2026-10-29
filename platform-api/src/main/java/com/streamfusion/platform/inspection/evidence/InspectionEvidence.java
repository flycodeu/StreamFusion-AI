package com.streamfusion.platform.inspection.evidence;

import com.baomidou.mybatisplus.annotation.IdType;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("inspection_evidence")
public class InspectionEvidence {
    @TableId(type = IdType.ASSIGN_ID)
    private Long id;

    private String evidenceId;
    private String runId;
    private Integer assignmentEpoch;
    private Long cameraId;
    private Long profileId;
    private String kind;
    private Integer placementRevision;
    private Long instanceId;
    private Long scopeId;
    private String bucket;
    private String objectKey;
    private Long endpointRevision;
    private Long sizeBytes;
    private String contentType;
    private String sha256;
    private String state;
    private String reasonCode;
    private String requestId;
    private LocalDateTime expiresAt;
    private LocalDateTime createdAt;
    private LocalDateTime updatedAt;
    private LocalDateTime committedAt;
    private String etag;
    private String objectVersionId;
}
