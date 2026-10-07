package com.streamfusion.platform.camera.pojo.entity;

import com.baomidou.mybatisplus.annotation.*;
import java.time.LocalDateTime;
import lombok.Getter;
import lombok.Setter;

@Getter
@Setter
@TableName("camera_source_endpoint")
public class CameraEndpointEntity {
    @TableId(type = IdType.ASSIGN_ID)
    private Long id;

    private Long sourceId;
    private String purpose;
    private String scheme;
    private String host;
    private Integer port;
    private String basePath;
    private String authMode;
    private Long credentialId;
    private String tlsPolicy;
    private String caBundleKey;
    private byte[] certSha256;
    private LocalDateTime createdAt;
    private LocalDateTime updatedAt;
}
