package com.streamfusion.platform.camera.status;

import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import com.streamfusion.platform.camera.mapper.CameraProfileMapper;
import com.streamfusion.platform.camera.service.CameraAccessService;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.pojo.dto.PageQueryDto;
import com.streamfusion.platform.common.validation.DecimalInput;
import java.time.Clock;
import java.time.LocalDateTime;
import java.time.ZoneId;
import java.util.List;
import lombok.Getter;
import lombok.RequiredArgsConstructor;
import lombok.Setter;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

@Service
@RequiredArgsConstructor
public class CameraStatusHistoryService {
    private final CameraStatusCheckMapper checks;
    private final CameraProfileMapper profiles;
    private final CameraAccessService access;
    private final Clock clock;

    @Getter
    @Setter
    public static class Query extends PageQueryDto {
        private String streamProfileId;
        private CameraStatusLayer layer = CameraStatusLayer.MEDIA;
        private int days = 7;
    }

    public record History(
            List<CameraStatusCheckVo> items,
            int total,
            int page,
            int size,
            int accessible,
            int failed,
            int unknown,
            boolean truncated) {}

    @Transactional(readOnly = true)
    public History history(String cameraId, Query query) {
        long camera = DecimalInput.id(cameraId, "cameraId");
        access.requireVisible(camera, access.readActor());
        if (query == null
                || query.getLayer() == null
                || query.getDays() < 1
                || query.getDays() > 30
                || query.getPage() > 50) throw BusinessException.error(ErrorCode.VALIDATION_ERROR);
        query.toPage();
        long profile = DecimalInput.id(query.getStreamProfileId(), "streamProfileId");
        var asset = profiles.selectById(profile);
        if (asset == null || asset.getChannelId() != camera)
            throw BusinessException.error(ErrorCode.NOT_FOUND);
        var now = LocalDateTime.ofInstant(clock.instant(), ZoneId.of("Asia/Shanghai"));
        // Profile/probe/time index and a fixed ceiling bound both database and response work.
        var rows =
                checks.selectList(
                        new LambdaQueryWrapper<CameraStatusCheckEntity>()
                                .eq(CameraStatusCheckEntity::getProfileId, profile)
                                .eq(CameraStatusCheckEntity::getCameraId, camera)
                                .eq(
                                        CameraStatusCheckEntity::getProbeKind,
                                        query.getLayer().probeKind())
                                .ge(
                                        CameraStatusCheckEntity::getRequestedAt,
                                        now.minusDays(query.getDays()))
                                .gt(CameraStatusCheckEntity::getRetainUntil, now)
                                .orderByDesc(
                                        CameraStatusCheckEntity::getRequestedAt,
                                        CameraStatusCheckEntity::getId)
                                .last("LIMIT 1001"));
        boolean truncated = rows.size() > 1000;
        if (truncated) rows = rows.subList(0, 1000);
        int accessible = 0, failed = 0;
        for (var row : rows) {
            if ("ACCESSIBLE".equals(row.getResultValue())) accessible++;
            if ("FAILED".equals(row.getResultValue())) failed++;
        }
        int start = Math.min(rows.size(), (query.getPage() - 1) * query.getSize());
        int end = Math.min(rows.size(), start + query.getSize());
        return new History(
                rows.subList(start, end).stream().map(CameraStatusCheckVo::from).toList(),
                rows.size(),
                query.getPage(),
                query.getSize(),
                accessible,
                failed,
                rows.size() - accessible - failed,
                truncated);
    }
}
