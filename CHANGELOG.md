# Changelog

## Unreleased

- **Breaking:** encrypted values now use `enc:v3:argon2id:<t>:<m>:<p>:...` with
  KDF parameters embedded in each value. Only `enc:v3:` is readable; values
  with other `enc:` prefixes fail loudly, as do configs mixing values from
  different keys or KDF-parameter eras (decrypt and re-encrypt to unify).
  Decrypt existing `enc:v2:` values with the previous enver version before
  upgrading, then re-run `enver encrypt`. The key file is compatible as-is.
