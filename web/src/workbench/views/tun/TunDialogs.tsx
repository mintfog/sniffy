import { useTranslation } from 'react-i18next'
import { detectPlatform } from '@/lib/platform'
import { ConfirmDialog } from '../../ui/ConfirmDialog'
import { InfoDialog } from '../../ui/InfoDialog'
import { closeUntrustedPrompt, dismissTunNotice, startTun, useTun } from '../../lib/tun'

/** TUN 模式的两个弹窗：开启时根证书未被信任的确认，与出错原因。挂在主窗口一次。 */
export function TunDialogs({ onInstallCert }: { onInstallCert: () => void }) {
  const { t } = useTranslation()
  const untrustedPrompt = useTun((s) => s.untrustedPrompt)
  const notice = useTun((s) => s.notice)
  const guide = notice?.code === 'elevation_unavailable' ? t(`tun.errors.elevationUnavailable.${detectPlatform()}`) : ''
  return (
    <>
      {untrustedPrompt && (
        <ConfirmDialog
          title={t('tun.untrustedTitle')}
          message={t('tun.untrustedMessage')}
          confirmLabel={t('tun.installCert')}
          cancelLabel={t('tun.cancel')}
          tone="primary"
          secondaryAction={{ label: t('tun.startAnyway'), onClick: startTun }}
          onConfirm={() => {
            closeUntrustedPrompt()
            onInstallCert()
          }}
          onClose={closeUntrustedPrompt}
        />
      )}
      {notice && (
        <InfoDialog
          title={t('tun.errorTitle')}
          message={[guide, notice.error, notice.stuck ? t('tun.stuckHint') : ''].filter(Boolean).join('\n\n')}
          tone="error"
          okLabel={t('tun.close')}
          onClose={dismissTunNotice}
        />
      )}
    </>
  )
}
