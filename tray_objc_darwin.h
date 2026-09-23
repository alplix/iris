#ifndef IRIS_TRAY_OBJC_DARWIN_H
#define IRIS_TRAY_OBJC_DARWIN_H

void iris_tray_start(const void *iconBytes, long iconLen,
                      const char *tooltip,
                      const char *showLabel, const char *showTip,
                      const char *refreshLabel, const char *refreshTip,
                      const char *hideLabel, const char *hideTip,
                      const char *quitLabel, const char *quitTip);

void iris_tray_stop(void);

void iris_tray_set_window_up(int up);

void iris_tray_relabel(const char *tooltip,
                        const char *showLabel, const char *showTip,
                        const char *refreshLabel, const char *refreshTip,
                        const char *hideLabel, const char *hideTip,
                        const char *quitLabel, const char *quitTip);

#endif
