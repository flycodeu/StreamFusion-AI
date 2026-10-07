package com.streamfusion.platform.camera.pojo.entity;

import com.baomidou.mybatisplus.annotation.IdType;
import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import java.math.BigDecimal;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

/** Configuration and the latest reliable observations have separate update semantics. */
@Getter
@Setter
@TableName("camera_stream_profile")
public class CameraProfileEntity {
    @TableId(type = IdType.ASSIGN_ID)
    private Long id;

    private Long sourceId;
    private Long channelId;
    private String externalProfileKey;
    private String label;
    private String sourceLabel;
    private String usageHint;
    private String usageOrigin;
    private Boolean enabled;
    private String videoCodec;
    private Integer width;
    private Integer height;
    private BigDecimal frameRate;
    private Long bitrateKbps;
    private String audioCodec;
    private Boolean hasAudio;
    private LocalDateTime parametersObservedAt;
    private String parametersOrigin;
    private Long version;
    private LocalDateTime createdAt;
    private LocalDateTime updatedAt;
    private Long createdBy;
    private Long updatedBy;
}
