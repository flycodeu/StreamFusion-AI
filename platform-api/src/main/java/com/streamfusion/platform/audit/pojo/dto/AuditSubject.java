package com.streamfusion.platform.audit.pojo.dto;

import lombok.Getter;
import lombok.Setter;

/** Deliberately narrow audit projection; never load credentials or full entity data. */
@Getter
@Setter
public class AuditSubject {
    private Long id;
    private String name;
    private String code;
}
