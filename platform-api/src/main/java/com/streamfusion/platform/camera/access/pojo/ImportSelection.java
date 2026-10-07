package com.streamfusion.platform.camera.access.pojo;

import io.swagger.v3.oas.annotations.media.Schema;
import java.util.List;

@Schema(description = "从有界发现快照选择通道和码流；候选ID仅在当前任务内有效")
public record ImportSelection(
        List<Selection> selections,
        String groupId,
        String confirmation,
        @com.fasterxml.jackson.annotation.JsonInclude(
                        com.fasterxml.jackson.annotation.JsonInclude.Include.NON_NULL)
                String targetCameraId,
        @com.fasterxml.jackson.annotation.JsonInclude(
                        com.fasterxml.jackson.annotation.JsonInclude.Include.NON_NULL)
                String targetCameraVersion) {
    public ImportSelection(List<Selection> selections, String groupId, String confirmation) {
        this(selections, groupId, confirmation, null, null);
    }

    public record Selection(String candidateId, List<String> profileIds, String defaultProfileId) {}
}
