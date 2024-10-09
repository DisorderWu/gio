# 1、解决modalLayer导致的大量CPU消耗
###### windows系统在有modallayer的时候，如果窗口最小化，可能导致大量CPU消耗，原因是gio源码中app/os_windows.go中的runLoop函数：
    anim := w.animating
	if anim && !windows.PeekMessage(msg, 0, 0, 0, windows.PM_NOREMOVE) {
			w.draw(false) 这里面判断最小化就不渲染
			continue
	}
    最小化的时候仍在不断调用peekmessage方法，导致大量CPU消耗。
###### 解决办法：
    修改在判断是否为animating的地方app/windows.go中的updateAnimation()函数，增加是否最小化的判断
    if w.decorations.Config.Size.X != 0 && w.decorations.Config.Size.Y != 0 {
		if w.hasNextFrame {
			if dt := time.Until(w.nextFrame); dt <= 0 {
				animate = true
			} else {
				// Schedule redraw.
				w.scheduleInvalidate(w.nextFrame)
			}
		}
	}