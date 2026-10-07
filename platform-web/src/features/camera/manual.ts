import type { ManualCameraCreate } from '../../api/camera/types'

export function newManualCamera() {
  return {
    name: '',
    remark: '',
    host: '',
    port: 80,
    scheme: 'http' as 'http' | 'https',
    adapterType: '',
    vendorHint: '',
    username: '',
    password: '',
  }
}
export type ManualCameraDraft = ReturnType<typeof newManualCamera>
export function manualCameraError(form: ManualCameraDraft) {
  if (!form.name.trim()) return '请填写相机名称。'
  if (!form.host.trim()) return '请填写设备 IP 或域名。'
  if (!Number.isInteger(form.port) || form.port < 1 || form.port > 65535)
    return '管理端口应在 1～65535 之间。'
  if (!!form.username !== !!form.password) return '设备账号与密码应一同填写，也可同时留空。'
  return ''
}
export function manualCameraInput(
  form: ManualCameraDraft,
): Omit<ManualCameraCreate, 'clientRequestId'> {
  return {
    name: form.name.trim(),
    remark: form.remark.trim() || null,
    connection: {
      host: form.host.trim(),
      port: form.port,
      scheme: form.scheme,
      ...(form.adapterType ? { adapterType: form.adapterType } : {}),
      ...(form.vendorHint.trim() ? { vendorHint: form.vendorHint.trim() } : {}),
      ...(form.username && form.password
        ? { username: form.username, password: form.password }
        : {}),
    },
  }
}
