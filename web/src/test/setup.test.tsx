import { useState } from 'react'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

function Counter() {
  const [count, setCount] = useState(0)
  return <button type="button" onClick={() => setCount((value) => value + 1)}>次数 {count}</button>
}

describe.sequential('Vitest 浏览器测试环境', () => {
  it('支持 render、jest-dom 和 userEvent', async () => {
    const user = userEvent.setup()
    render(<Counter />)

    const button = screen.getByRole('button', { name: '次数 0' })
    expect(button).toBeInTheDocument()

    await user.click(button)
    expect(button).toHaveTextContent('次数 1')

    localStorage.setItem('test-key', 'value')
    document.documentElement.lang = 'zh-CN'
    document.title = '测试标题'
  })

  it('在测试后清理 DOM、存储和文档状态', () => {
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    expect(localStorage.getItem('test-key')).toBeNull()
    expect(document.documentElement.lang).toBe('')
    expect(document.title).toBe('')
  })
})
