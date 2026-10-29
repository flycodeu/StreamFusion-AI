package com.streamfusion.platform.inspection.evidence;

import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import jakarta.servlet.http.HttpServletRequest;
import java.util.Map;
import lombok.RequiredArgsConstructor;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestHeader;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.servlet.mvc.method.annotation.StreamingResponseBody;

@RestController
@RequestMapping("/inspection-nodes/evidence")
@RequiredArgsConstructor
public class InspectionEvidenceController {
    private final InspectionEvidenceService evidence;
    private final InspectionEvidenceReadService reads;

    @PostMapping("/authorize")
    public R<InspectionEvidenceCommands.Grant> authorize(
            @RequestBody InspectionEvidenceCommands.Authorize input, HttpServletRequest request) {
        return R.success(evidence.authorize(node(request), input));
    }

    @PostMapping("/complete")
    public R<InspectionEvidenceCommands.Completion> complete(
            @RequestBody InspectionEvidenceCommands.Complete input, HttpServletRequest request) {
        return R.success(evidence.complete(node(request), input));
    }

    @GetMapping("/{evidenceId}")
    @ModuleAccess("camera")
    public R<Map<String, Object>> detail(@PathVariable String evidenceId) {
        return R.success(reads.detail(evidenceId));
    }

    @GetMapping("/{evidenceId}/content")
    @ModuleAccess("camera")
    public ResponseEntity<StreamingResponseBody> content(
            @PathVariable String evidenceId,
            @RequestParam(defaultValue = "inline") String disposition,
            @RequestHeader(value = "Range", required = false) String range) {
        return reads.content(evidenceId, disposition, range);
    }

    private static String node(HttpServletRequest request) {
        return (String) request.getAttribute(InspectionNodeAuthentication.NODE_ID);
    }
}
