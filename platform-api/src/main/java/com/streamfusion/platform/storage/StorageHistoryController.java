package com.streamfusion.platform.storage;

import com.streamfusion.platform.common.response.R;
import com.streamfusion.platform.common.security.ModuleAccess;
import lombok.RequiredArgsConstructor;
import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/storage")
@ModuleAccess("storage")
@RequiredArgsConstructor
public class StorageHistoryController {
    private final StorageHistoryPolicyService policies;

    @GetMapping("/history-policy")
    public R<?> global() {
        return R.success(policies.detail(0));
    }

    @PutMapping("/history-policy")
    public R<?> saveGlobal(@RequestBody StorageHistoryPolicyService.Command input) {
        return R.success(policies.save(0, input));
    }

    @GetMapping("/instances/{id}/history-policy")
    public R<?> instance(@PathVariable long id) {
        return R.success(policies.detail(id));
    }

    @PutMapping("/instances/{id}/history-policy")
    public R<?> saveInstance(
            @PathVariable long id, @RequestBody StorageHistoryPolicyService.Command input) {
        return R.success(policies.save(id, input));
    }
}
