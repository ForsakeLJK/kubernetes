/* Copyright The Kubernetes Authors.
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
#include <errno.h>
#include <fcntl.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>
#ifdef __linux__
#include <sys/syscall.h>
#include <sys/vfs.h>
#include <linux/magic.h>
#endif

static const char *directory = "/tmp/numa-evidence";

static FILE *artifact(const char *name)
{
    char path[4096];
    if (snprintf(path, sizeof(path), "%s/%s", directory, name) >= (int)sizeof(path)) {
        fprintf(stderr, "evidence path too long\n");
        exit(2);
    }
    FILE *file = fopen(path, "wx");
    if (!file) { perror(path); exit(2); }
    return file;
}

static void finish(FILE *file)
{
    int error = ferror(file);
    if (fclose(file) != 0 || error) {
        fprintf(stderr, "cannot persist probe evidence under %s: %s\n", directory, strerror(errno));
        exit(2);
    }
}

static size_t quantity(const char *text)
{
    char *end;
    errno = 0;
    unsigned long long value = strtoull(text, &end, 10);
    if (errno || *end || text[0] < '0' || text[0] > '9' || !value || value > SIZE_MAX) {
        fprintf(stderr, "invalid positive byte quantity: %s\n", text);
        exit(2);
    }
    return (size_t)value;
}

static size_t hugepage_size(const char *text)
{
    char *end;
    errno = 0;
    unsigned long long value = strtoull(text, &end, 10);
    size_t unit = !strcmp(end, "Ki") ? 1024 : !strcmp(end, "Mi") ? 1024 * 1024 :
                  !strcmp(end, "Gi") ? 1024 * 1024 * 1024 : 0;
    if (errno || text[0] < '1' || text[0] > '9' || !unit || value > SIZE_MAX / unit) {
        fprintf(stderr, "hugepage size must be a positive Ki/Mi/Gi quantity: %s\n", text);
        exit(2);
    }
    return (size_t)value * unit;
}

static void proc_evidence(void)
{
#ifdef __linux__
    const char *names[] = {"status", "maps", "numa_maps"};
    for (int i = 0; i < 3; i++) {
        char path[64], name[64];
        snprintf(path, sizeof(path), "/proc/self/%s", names[i]);
        FILE *input = fopen(path, "r");
        if (!input) { perror(path); continue; }
        snprintf(name, sizeof(name), "%s.txt", names[i]);
        FILE *output = artifact(name);
        char buffer[4096];
        size_t count;
        while ((count = fread(buffer, 1, sizeof(buffer), input))) {
            if (fwrite(buffer, 1, count, output) != count) { perror(name); exit(2); }
        }
        if (ferror(input)) { perror(path); exit(2); }
        fclose(input);
        finish(output);
    }
#endif
}

static void observe(FILE *file, void *mapping, size_t bytes, const char *name, int comma)
{
    size_t page = (size_t)sysconf(_SC_PAGESIZE);
    size_t total = (bytes - 1) / page + 1;
    size_t samples = total < 4096 ? total : 4096;
    void **addresses = calloc(samples, sizeof(void *));
    int *nodes = calloc(samples, sizeof(int));
    if (!addresses || !nodes) { fprintf(stderr, "cannot allocate query buffers\n"); exit(2); }
    for (size_t i = 0; i < samples; i++) {
        size_t index = samples == 1 ? 0 : i * (total - 1) / (samples - 1);
        addresses[i] = (char *)mapping + index * page;
    }
    int error = ENOSYS;
#if defined(__linux__) && defined(SYS_move_pages)
    /* NULL nodes and flags zero query locations, without binding or migration. */
    errno = 0;
    long result = syscall(SYS_move_pages, 0, samples, addresses, NULL, nodes, 0);
    error = result < 0 ? errno : 0;
#endif
    fprintf(file, "%s{\"name\":\"%s\",\"backing\":\"%s\",\"start\":\"0x%llx\",\"bytes\":%zu,"
            "\"total_pages\":%zu,\"sampled_pages\":%zu,\"errno\":%d,\"page_nodes\":[",
            comma ? "," : "", name, comma ? "hugetlbfs" : "private-anonymous", (unsigned long long)(uintptr_t)mapping,
            bytes, total, samples, error);
    for (size_t i = 0; !error && i < samples; i++) fprintf(file, "%s%d", i ? "," : "", nodes[i]);
    fprintf(file, "]}");
    free(addresses);
    free(nodes);
}

int main(int argc, char **argv)
{
    size_t bytes = 16777216;
    unsigned int hold = 3600;
    const char *release = NULL;
    const char *container = "standalone";
    const char *paths[16], *names[16];
    size_t sizes[16];
    size_t page_sizes[16];
    int hugepages = 0;
    for (int i = 1; i < argc; i++) {
        if (!strcmp(argv[i], "--bytes") && i + 1 < argc) bytes = quantity(argv[++i]);
        else if (!strcmp(argv[i], "--output") && i + 1 < argc) directory = argv[++i];
        else if (!strcmp(argv[i], "--container") && i + 1 < argc) {
            container = argv[++i];
            if (strspn(container, "abcdefghijklmnopqrstuvwxyz0123456789-") != strlen(container)) {
                fprintf(stderr, "container name must contain only lowercase letters, digits and hyphens\n"); return 2;
            }
        }
        else if (!strcmp(argv[i], "--release") && i + 1 < argc) release = argv[++i];
        else if (!strcmp(argv[i], "--hold-seconds") && i + 1 < argc) {
            const char *value = argv[++i];
            size_t seconds = !strcmp(value, "0") ? 0 : quantity(value);
            if (seconds > 86400) { fprintf(stderr, "hold must be 0..86400 seconds\n"); return 2; }
            hold = (unsigned int)seconds;
        } else if (!strcmp(argv[i], "--hugepage") && i + 3 < argc && hugepages < 16) {
            names[hugepages] = argv[++i];
            page_sizes[hugepages] = hugepage_size(names[hugepages]);
            paths[hugepages] = argv[++i];
            sizes[hugepages] = quantity(argv[++i]);
            if (sizes[hugepages] % page_sizes[hugepages]) {
                fprintf(stderr, "hugepage bytes must be a multiple of the page size\n"); return 2;
            }
            hugepages++;
        } else {
            fprintf(stderr, "usage: probe [--bytes N] [--output DIR] [--hold-seconds N] "
                    "[--release PATH] [--hugepage SIZE PATH BYTES]...\n");
            return 2;
        }
    }
    if (mkdir(directory, 0700) != 0) { perror(directory); return 2; }
    FILE *file = artifact("observation.json");
    fprintf(file, "{\"schema_version\":1,\"container\":\"%s\",\"pid\":%ld,\"observed_unix\":%lld,"
            "\"method\":\"move_pages query only\",\"page_size\":%ld,\"mappings\":[",
            container, (long)getpid(), (long long)time(NULL), sysconf(_SC_PAGESIZE));
    void *mappings[17];
    size_t lengths[17];
    int failed = 0, allocated = 0;
    for (int i = 0; i <= hugepages; i++) {
        int fd = -1, error = 0;
        size_t length = i ? sizes[i - 1] : bytes;
        const char *name = i ? names[i - 1] : "anonymous";
        if (i) {
            fd = open(paths[i - 1], O_CREAT | O_RDWR, 0600);
            if (fd < 0) error = errno;
#ifdef __linux__
            struct statfs fs;
            if (!error && fstatfs(fd, &fs)) error = errno;
            if (!error && (fs.f_type != HUGETLBFS_MAGIC || (size_t)fs.f_bsize != page_sizes[i - 1])) error = EINVAL;
#else
            if (!error) error = ENOTSUP;
#endif
            if (!error && ftruncate(fd, (off_t)length)) error = errno;
        }
        void *mapping = MAP_FAILED;
        if (!error) mapping = mmap(NULL, length, PROT_READ | PROT_WRITE,
                                  i ? MAP_SHARED : MAP_PRIVATE | MAP_ANONYMOUS, fd, 0);
        if (mapping == MAP_FAILED && !error) error = errno;
        if (fd >= 0) close(fd);
        if (error) {
            fprintf(stderr, "allocation %s (%zu bytes) failed: %s\n", name, length, strerror(error));
            fprintf(file, "%s{\"name\":\"%s\",\"allocation_errno\":%d}", i ? "," : "", name, error);
            failed = 1;
            break;
        }
        /* Volatile writes fault private anonymous/hugetlb pages before querying. */
        volatile char *touched = mapping;
        size_t page = (size_t)sysconf(_SC_PAGESIZE);
        for (size_t offset = 0; offset < length; offset += page) touched[offset] = 1;
        touched[length - 1] = 1;
        observe(file, mapping, length, name, i);
        mappings[allocated] = mapping;
        lengths[allocated++] = length;
    }
    fprintf(file, "],\"allocation_status\":\"%s\",\"phase\":\"touched\"}\n", failed ? "FAIL" : "PASS");
    finish(file);
    proc_evidence();
    if (!failed) {
        while (hold-- && (!release || access(release, F_OK) != 0)) sleep(1);
    }
    for (int i = 0; i < allocated; i++) munmap(mappings[i], lengths[i]);
    /* Init volumes survive completion; release their pages before apps reuse accounting. */
    for (int i = 0; i < allocated - 1; i++) unlink(paths[i]);
    return failed ? 1 : 0;
}
