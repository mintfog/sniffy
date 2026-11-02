import { useTranslation } from 'react-i18next'
import { useRef, useState } from 'react'
import { X } from 'lucide-react'
import { IosProxyImage, type IosProxyImageValues } from './IosProxyImage'

export function GuideImage({
  images,
  proxy,
}: {
  images: { url: string; alt: string }[]
  proxy?: IosProxyImageValues
}) {
  const { t } = useTranslation()
  const previewRef = useRef<HTMLDialogElement>(null)
  const [active, setActive] = useState(0)
  const current = images[Math.min(active, images.length - 1)]
  const picture = (className?: string) =>
    proxy ? (
      <IosProxyImage
        url={current.url}
        alt={current.alt}
        className={className}
        {...proxy}
      />
    ) : (
      <img
        className={className}
        src={current.url}
        alt={current.alt}
        draggable={false}
      />
    )
  return (
    <div className="cg-image-set">
      {images.length > 1 && (
        <div
          className="cg-shot-pages"
          role="group"
          aria-label={t('certs.guideDialog.imageSequence')}
        >
          {images.map((image, index) => (
            <button
              key={image.url}
              type="button"
              aria-pressed={index === active}
              onClick={() => setActive(index)}
            >
              {index + 1} · {image.alt}
            </button>
          ))}
        </div>
      )}
      <button
        type="button"
        className="cg-shot-trigger"
        aria-label={`${t('certs.guideDialog.imageEnlarge')}: ${current.alt}`}
        aria-haspopup="dialog"
        onClick={() => previewRef.current?.showModal()}
      >
        {picture('cg-shot')}
        <span>{t('certs.guideDialog.imageEnlarge')}</span>
      </button>
      <dialog
        ref={previewRef}
        className="cg-image-preview"
        aria-label={current.alt}
        onKeyDown={e => {
          // Escape 只关闭大图，避免触发外层安装引导的窗口级监听。
          if (e.key === 'Escape') e.stopPropagation()
        }}
        onClick={e => {
          if (e.target === e.currentTarget) previewRef.current?.close()
        }}
      >
        <button
          type="button"
          className="cg-image-close"
          aria-label={t('certs.guideDialog.close')}
          autoFocus
          onClick={() => previewRef.current?.close()}
        >
          <X />
        </button>
        {picture()}
      </dialog>
    </div>
  )
}
