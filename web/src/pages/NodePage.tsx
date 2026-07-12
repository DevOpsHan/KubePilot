import { useParams, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { ArrowLeft } from 'lucide-react'
import { NodeList } from '@/components/NodeList'
import { Button } from '@/components/ui/button'

// 独立节点列表页(集群详情之外的直达入口)
export function NodePage() {
  const { t } = useTranslation('clusters')
  const { id } = useParams()
  const navigate = useNavigate()
  const clusterId = id ? Number(id) : undefined
  return (
    <div className="px-8 py-7">
      <Button variant="ghost" size="sm" className="mb-3" onClick={() => navigate(`/clusters/${id ?? ''}`)}><ArrowLeft className="size-4" /> {t('detail.backToDetail')}</Button>
      <NodeList clusterId={clusterId} />
    </div>
  )
}
