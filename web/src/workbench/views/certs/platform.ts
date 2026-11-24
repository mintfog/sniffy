import { detectPlatform } from '@/lib/platform'

export type CertPlatform =
  | 'windows'
  | 'macos'
  | 'linux'
  | 'ios'
  | 'android'
  | 'firefox'

export function hostCertPlatform(): CertPlatform {
  const platform = detectPlatform()
  return platform === 'mac' ? 'macos' : platform
}
