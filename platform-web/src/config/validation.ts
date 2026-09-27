/** Input feedback only; the backend remains responsible for validating every request. */
export const loginValidation = {
  account: { minLength: 4, maxLength: 32, pattern: /^[A-Za-z0-9]+$/ },
  // Login accepts existing credentials; new-password strength comes from /auth/password-policy.
  password: { minLength: 8, maxLength: 128 },
  captcha: { length: 5, pattern: /^[A-Za-z0-9]{5}$/ },
} as const
