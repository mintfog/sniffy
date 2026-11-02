import { detectPlatform } from '@/lib/platform'

export type CertPlatform = 'windows' | 'macos' | 'ios' | 'android' | 'firefox'

export function hostCertPlatform(): CertPlatform {
  return detectPlatform() === 'mac' ? 'macos' : 'windows'
}
