import { describe, expect, it } from 'vitest'
import { codecFromSps, findStartCode, FrameAssembler, joinAnnexB, nalType, splitNals } from './h264'

const sps = new Uint8Array([0x67, 0x42, 0xc0, 0x1f, 0xaa])
const pps = new Uint8Array([0x68, 0xce, 0x3c])
const idr = new Uint8Array([0x65, 0x88, 0x01])   // 第一个比特 1：新的一帧
const p1 = new Uint8Array([0x41, 0x9a, 0x02])
const p1b = new Uint8Array([0x41, 0x1a, 0x03])  // 同一帧的第二片（first_mb_in_slice ≠ 0）
const p2 = new Uint8Array([0x41, 0x9b, 0x04])

describe('h264', () => {
  it('finds 3- and 4-byte start codes', () => {
    expect(findStartCode(new Uint8Array([9, 0, 0, 1, 5]), 0)).toEqual({ at: 1, len: 3 })
    expect(findStartCode(new Uint8Array([0, 0, 0, 1, 5]), 0)).toEqual({ at: 0, len: 4 })
    expect(findStartCode(new Uint8Array([0, 0, 2]), 0)).toBeUndefined()
  })

  it('splits a stream into NALs and keeps the unfinished tail', () => {
    const stream = joinAnnexB([sps, pps, idr])
    const { nals, rest } = splitNals(stream)
    expect(nals.map(nalType)).toEqual([7, 8])
    expect(Array.from(rest)).toEqual([0, 0, 0, 1, ...idr])
  })

  it('reads the codec from SPS', () => {
    expect(codecFromSps(sps)).toBe('avc1.42c01f')
  })

  it('groups NALs into frames, parameter sets go with the key frame', () => {
    const a = new FrameAssembler()
    const out = [sps, pps, idr, p1, p1b, p2, p2].map((n) => a.push(n)).filter(Boolean)
    expect(out).toHaveLength(3)
    expect(out[0]).toEqual({ key: true, data: joinAnnexB([sps, pps, idr]) })
    expect(out[1]).toEqual({ key: false, data: joinAnnexB([p1, p1b]) })
    expect(out[2]?.key).toBe(false)
    expect(a.sps).toEqual(sps)
  })
})
