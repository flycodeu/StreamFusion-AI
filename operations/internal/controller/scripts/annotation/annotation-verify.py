owner()
expected = load(backup/'expected.json')
if actual() != expected:
    fail('实际配置与本次操作不一致')
health(expected)
print('实际配置与服务状态核对通过', flush=True)
