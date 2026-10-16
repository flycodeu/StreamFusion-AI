package com.streamfusion.platform.media.preview;

import com.streamfusion.platform.common.pojo.vo.PageResultVo;
import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import io.swagger.v3.oas.annotations.Operation;
import io.swagger.v3.oas.annotations.tags.Tag;
import lombok.RequiredArgsConstructor;
import org.springframework.http.HttpStatus;
import org.springframework.web.bind.annotation.DeleteMapping;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.ModelAttribute;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.ResponseStatus;
import org.springframework.web.bind.annotation.RestController;

@Tag(name = "视频预览")
@RestController
@RequiredArgsConstructor
@RequestMapping("/preview")
@ModuleAccess("camera")
public class PreviewController {
    private final PreviewCatalogService catalog;
    private final PreviewLeaseService leases;

    @GetMapping("/catalog")
    @Operation(summary = "读取当前账户可见的预览目录")
    public R<PageResultVo<PreviewModels.Channel>> catalog(
            @ModelAttribute PreviewModels.CatalogQuery query) {
        return R.success(catalog.catalog(query));
    }

    @PostMapping("/leases")
    @ResponseStatus(HttpStatus.CREATED)
    @Operation(summary = "创建短期预览租约并按需接入ZLM")
    public R<PreviewModels.LeaseView> create(@RequestBody PreviewModels.CreateLease input) {
        return R.success(leases.create(input));
    }

    @PostMapping("/leases/{leaseId}/renew")
    @Operation(summary = "续期预览租约")
    public R<PreviewModels.LeaseView> renew(@PathVariable String leaseId) {
        return R.success(leases.renew(leaseId));
    }

    @PostMapping("/leases/{leaseId}/webrtc")
    @Operation(summary = "代理ZLM WebRTC播放信令")
    public R<PreviewModels.Answer> webrtc(
            @PathVariable String leaseId, @RequestBody PreviewModels.Offer offer) {
        return R.success(leases.answer(leaseId, offer));
    }

    @GetMapping("/leases/{leaseId}/stats")
    @Operation(summary = "读取预览媒体轨道和速率观测")
    public R<PreviewModels.Stats> stats(@PathVariable String leaseId) {
        return R.success(leases.stats(leaseId));
    }

    @DeleteMapping("/leases/{leaseId}")
    @Operation(summary = "释放预览租约及其ZLM流")
    public R<Void> delete(@PathVariable String leaseId) {
        leases.delete(leaseId);
        return R.success();
    }
}
