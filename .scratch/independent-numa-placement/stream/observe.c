/* Copyright 2026 The Kubernetes Authors.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 * http://www.apache.org/licenses/LICENSE-2.0
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
#include "observe.h"
#include <errno.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#ifdef __linux__
#include <sched.h>
#include <sys/syscall.h>
#endif
#ifdef _OPENMP
#include <omp.h>
#endif

static FILE *artifact(const char *name)
{
    char path[4096];
    const char *directory = getenv("STREAM_RUN_DIR");
    if (!directory || snprintf(path, sizeof(path), "%s/%s", directory, name) >= (int)sizeof(path)) {
        fprintf(stderr, "STREAM_RUN_DIR is missing or too long\n");
        exit(3);
    }
    FILE *file = fopen(path, "w");
    if (!file) { perror(path); exit(3); }
    return file;
}

static void finish(FILE *file)
{
    if (ferror(file) || fclose(file) != 0) {
        fprintf(stderr, "cannot persist STREAM observation artifact\n");
        exit(3);
    }
}

static void proc_evidence(void)
{
#ifdef __linux__
    const char *names[] = {"status", "maps", "numa_maps"};
    for (int i = 0; i < 3; i++) {
        char path[64];
        char name[64];
        snprintf(path, sizeof(path), "/proc/self/%s", names[i]);
        snprintf(name, sizeof(name), "proc-%s.txt", names[i]);
        FILE *input = fopen(path, "r");
        if (!input) {
            fprintf(stderr, "cannot read %s: %s\n", path, strerror(errno));
            continue;
        }
        FILE *output = artifact(name);
        char buffer[4096];
        size_t count;
        while ((count = fread(buffer, 1, sizeof(buffer), input))) {
            if (fwrite(buffer, 1, count, output) != count) {
                fprintf(stderr, "cannot persist %s\n", name);
                exit(3);
            }
        }
        if (ferror(input)) fprintf(stderr, "incomplete read of %s\n", path);
        fclose(input);
        finish(output);
    }
#endif
}

static void threads(void)
{
    FILE *file = artifact("threads.json");
    int first = 1;
    fprintf(file, "{\"threads\":[");
#ifdef _OPENMP
#pragma omp parallel
#endif
    {
#ifdef _OPENMP
        int tid = omp_get_thread_num();
#pragma omp critical
#else
        int tid = 0;
#endif
        {
            fprintf(file, "%s{\"thread\":%d", first ? "" : ",", tid);
            first = 0;
#ifdef __linux__
            /* Query each worker, since the main thread's mask proves no worker placement. */
            int count = (int)sysconf(_SC_NPROCESSORS_CONF);
            if (count < 1024) count = 1024;
            cpu_set_t *mask = CPU_ALLOC(count);
            size_t size = CPU_ALLOC_SIZE(count);
            if (mask && sched_getaffinity(0, size, mask) == 0) {
                fprintf(file, ",\"cpu\":%d,\"allowed_cpus\":[", sched_getcpu());
                int comma = 0;
                for (int cpu = 0; cpu < count; cpu++) {
                    if (CPU_ISSET_S(cpu, size, mask)) {
                        fprintf(file, "%s%d", comma ? "," : "", cpu);
                        comma = 1;
                    }
                }
                fprintf(file, "]");
            } else {
                fprintf(file, ",\"affinity_error\":%d", errno);
            }
            CPU_FREE(mask);
#else
            fprintf(file, ",\"affinity_error\":\"not Linux\"");
#endif
            fprintf(file, "}");
        }
    }
    fprintf(file, "]}\n");
    finish(file);
}

static void residency(void **arrays, size_t bytes)
{
    FILE *file = artifact("residency.json");
#if defined(__linux__) && defined(SYS_move_pages)
    size_t page = (size_t)sysconf(_SC_PAGESIZE);
    size_t limit = (size_t)strtoull(getenv("STREAM_RESIDENCY_SAMPLES"), NULL, 10);
    int available = 1;
    fprintf(file, "{\"method\":\"move_pages query only\",\"arrays\":[");
    for (int array = 0; array < 3; array++) {
        uintptr_t start = (uintptr_t)arrays[array];
        uintptr_t base = start - start % page;
        size_t total = (start + bytes - 1 - base) / page + 1;
        size_t count = total < limit ? total : limit;
        void **addresses = calloc(count, sizeof(void *));
        int *status = calloc(count, sizeof(int));
        if (!addresses || !status) { fprintf(stderr, "residency sample allocation failed\n"); exit(3); }
        for (size_t i = 0; i < count; i++) {
            size_t index = count == 1 ? 0 : i * (total - 1) / (count - 1);
            addresses[i] = (void *)(base + index * page);
        }
        errno = 0;
        /* NULL nodes queries placement; it neither binds nor migrates any page. */
        long result = syscall(SYS_move_pages, 0, count, addresses, NULL, status, 0);
        int error = result < 0 ? errno : 0;
        if (error) available = 0;
        fprintf(file, "%s{\"name\":\"%c\",\"total_pages\":%zu,\"sampled_pages\":%zu,\"errno\":%d,\"page_status\":[",
                array ? "," : "", 'a' + array, total, count, error);
        for (size_t i = 0; !error && i < count; i++) {
            if (status[i] < 0) available = 0;
            fprintf(file, "%s%d", i ? "," : "", status[i]);
        }
        fprintf(file, "]}");
        free(addresses);
        free(status);
    }
    fprintf(file, "],\"status\":\"%s\",\"page_size\":%zu}\n", available ? "sampled" : "unavailable", page);
#else
    (void)arrays;
    (void)bytes;
    fprintf(file, "{\"status\":\"unavailable\",\"reason\":\"move_pages query requires Linux\"}\n");
#endif
    finish(file);
}

void stream_observe(void *a, void *b, void *c, size_t bytes)
{
    void *arrays[] = {a, b, c};
    unsigned int hold = (unsigned int)strtoul(getenv("STREAM_OBSERVE_SECONDS"), NULL, 10);
    FILE *file = artifact("arrays.json");
    fprintf(file, "{\"pid\":%ld,\"phase\":\"initialized_before_calibration\",\"hold_seconds\":%u,\"arrays\":[",
            (long)getpid(), hold);
    for (int i = 0; i < 3; i++) {
        fprintf(file, "%s{\"name\":\"%c\",\"start\":\"0x%llx\",\"end\":\"0x%llx\",\"bytes\":%zu}",
                i ? "," : "", 'a' + i, (unsigned long long)(uintptr_t)arrays[i],
                (unsigned long long)((uintptr_t)arrays[i] + bytes), bytes);
    }
    fprintf(file, "]}\n");
    finish(file);
    threads();
    proc_evidence();
    fflush(stdout);
    fprintf(stderr, "STREAM arrays initialized; PID %ld; observation window %u seconds\n", (long)getpid(), hold);
    while (hold) hold = sleep(hold);
    residency(arrays, bytes);
}
