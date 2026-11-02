import { useTranslation } from 'react-i18next'

export interface IosProxyImageValues {
  host: string
  port: string
  language: string
}

export function IosProxyImage({
  url,
  alt,
  className,
  host,
  port,
  language,
}: IosProxyImageValues & { url: string; alt: string; className?: string }) {
  const { t } = useTranslation()
  const english = language === 'en'

  return (
    <svg
      className={className}
      width="1179"
      height="1190"
      viewBox="0 0 1179 1190"
      preserveAspectRatio="xMidYMid meet"
      role="img"
      aria-label={`${alt}; ${t('certs.guide.proxy.server')}: ${host}; ${t('certs.guide.proxy.port')}: ${port}`}
    >
      <image href={url} width="1179" height="1190" />
      {/* 坐标为截图文件的像素坐标；文字与底图同在 SVG 坐标系中，缩放和放大时保持对齐。 */}
      <g
        fill="#000"
        fontFamily="Arial, sans-serif"
        textAnchor="end"
        dominantBaseline="central"
      >
        <text
          x="1082"
          y={english ? 805.5 : 815.5}
          fontSize={Math.min(50, 1000 / Math.max(1, host.length))}
        >
          {host}
        </text>
        <text x="1082" y={english ? 937.5 : 946.5} fontSize="50">
          {port}
        </text>
      </g>
    </svg>
  )
}
