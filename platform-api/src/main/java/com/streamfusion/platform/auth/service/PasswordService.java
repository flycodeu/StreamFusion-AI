package com.streamfusion.platform.auth.service;

import com.streamfusion.platform.auth.config.AuthProperties;
import com.streamfusion.platform.common.exception.BusinessException;
import com.streamfusion.platform.common.exception.ErrorCode;
import com.streamfusion.platform.common.exception.details.ValidationDetails;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.Semaphore;
import org.springframework.security.crypto.password.PasswordEncoder;
import org.springframework.stereotype.Service;

@Service
public class PasswordService {
    private final PasswordEncoder encoder;
    private final AuthProperties properties;
    private final Semaphore hashSlots = new Semaphore(4);
    private final String dummyHash;

    public PasswordService(PasswordEncoder encoder, AuthProperties properties) {
        this.encoder = encoder;
        this.properties = properties;
        this.dummyHash = encoder.encode(UUID.randomUUID().toString());
    }

    public void validateNewPassword(String password) {
        if (password == null) invalid();
        if (password.length() > properties.maxPasswordLength() * 2) invalid();
        int length = password.codePointCount(0, password.length());
        if (length < properties.minPasswordLength() || length > properties.maxPasswordLength())
            invalid();
        boolean upper = false, lower = false, digit = false, symbol = false;
        for (int cp : password.codePoints().toArray()) {
            if (Character.isISOControl(cp)
                    || Character.isWhitespace(cp)
                    || Character.isSpaceChar(cp)) invalid();
            upper |= cp >= 'A' && cp <= 'Z';
            lower |= cp >= 'a' && cp <= 'z';
            digit |= cp >= '0' && cp <= '9';
            symbol |= !Character.isLetterOrDigit(cp);
        }
        if (properties.requireUppercase() && !upper
                || properties.requireLowercase() && !lower
                || properties.requireDigit() && !digit
                || properties.requireSymbol() && !symbol) invalid();
    }

    public String encode(String password) {
        acquire();
        try {
            return encoder.encode(password);
        } finally {
            hashSlots.release();
        }
    }

    public boolean matches(String password, String hash) {
        if (password == null || password.length() > 4096) return false;
        acquire();
        try {
            try {
                return encoder.matches(password, hash);
            } catch (IllegalArgumentException ex) {
                return false;
            }
        } finally {
            hashSlots.release();
        }
    }

    public void verifyDummy(String password) {
        matches(password, dummyHash);
    }

    public String initialPasswordHash() {
        String value = properties.initialPassword();
        if (value == null || value.isBlank()) {
            throw BusinessException.error(ErrorCode.INITIAL_PASSWORD_UNAVAILABLE);
        }
        try {
            validateNewPassword(value);
        } catch (BusinessException ex) {
            throw BusinessException.error(ErrorCode.INITIAL_PASSWORD_UNAVAILABLE);
        }
        return encode(value);
    }

    public void validateReplacement(String currentPassword, String newPassword) {
        validateNewPassword(newPassword);
        if (newPassword.equals(currentPassword)
                || (!properties.initialPassword().isEmpty()
                        && newPassword.equals(properties.initialPassword()))) invalid();
    }

    private void acquire() {
        if (!hashSlots.tryAcquire()) throw BusinessException.error(ErrorCode.RATE_LIMITED);
    }

    private static void invalid() {
        throw BusinessException.error(
                ErrorCode.VALIDATION_ERROR,
                new ValidationDetails(
                        List.of(ValidationDetails.FieldError.invalid("newPassword"))));
    }
}
