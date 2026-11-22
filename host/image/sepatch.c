// sepatch：给编译好的 SELinux 规则（二进制 policy）加几条规则（踩坑 #135），原理和 magiskpolicy 一样，用官方 libsepol。
//
// 用法：sepatch <输入 policy> <输出 policy> <规则文件>
// 规则文件每行一条：
//   permissive <类型>                         这个域只记录不拦截
//   allow <源类型或属性> <目标类型或属性> <类> <权限>[,<权限>...]
//   mlstrusted <类型>                          当成 mlstrustedobject（多级安全约束里「可信对象」）：带分类的 App 也能写它。
//                                              约束在编译时已经展开成具体类型，所以直接改约束表达式，不只是加属性
// 编译（Debian：apt install libsepol-dev）：gcc -O2 -o sepatch sepatch.c -l:libsepol.a（内部函数只在静态库里导出）
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sepol/policydb/policydb.h>
#include <sepol/policydb/avtab.h>
#include <sepol/policydb/services.h>
#include <sepol/policydb/conditional.h>
#include <sepol/policydb/constraint.h>

static policydb_t pdb;

static type_datum_t *type_of(const char *name) {
	type_datum_t *t = hashtab_search(pdb.p_types.table, (hashtab_key_t)name);
	if (!t) fprintf(stderr, "sepatch: 规则里没有类型 %s\n", name);
	return t;
}

static int perm_value(class_datum_t *c, const char *perm) {
	perm_datum_t *p = hashtab_search(c->permissions.table, (hashtab_key_t)perm);
	if (!p && c->comdatum) p = hashtab_search(c->comdatum->permissions.table, (hashtab_key_t)perm);
	return p ? (int)p->s.value : 0;
}

static int add_allow(const char *src, const char *tgt, const char *cls, char *perms) {
	type_datum_t *s = type_of(src), *t = type_of(tgt);
	class_datum_t *c = hashtab_search(pdb.p_classes.table, (hashtab_key_t)cls);
	if (!s || !t) return -1;
	if (!c) { fprintf(stderr, "sepatch: 规则里没有类 %s\n", cls); return -1; }
	uint32_t bits = 0;
	for (char *p = strtok(perms, ","); p; p = strtok(NULL, ",")) {
		int v = perm_value(c, p);
		if (!v) { fprintf(stderr, "sepatch: 类 %s 没有权限 %s\n", cls, p); return -1; }
		bits |= 1U << (v - 1);
	}
	avtab_key_t key = { .source_type = s->s.value, .target_type = t->s.value, .target_class = c->s.value, .specified = AVTAB_ALLOWED };
	avtab_datum_t *d = avtab_search(&pdb.te_avtab, &key);
	if (d) {
		d->data |= bits;
	} else {
		avtab_datum_t nd = { .data = bits };
		if (avtab_insert(&pdb.te_avtab, &key, &nd)) { fprintf(stderr, "sepatch: 加规则失败\n"); return -1; }
	}
	return 0;
}

// 约束表达式里「目标类型属于 mlstrustedobject」的那些，把 t 也加进去
static int add_mlstrusted(const char *name) {
	type_datum_t *t = type_of(name), *attr = type_of("mlstrustedobject");
	if (!t || !attr) return -1;
	int hit = 0;
	for (uint32_t i = 0; i < pdb.p_classes.nprim; i++) {
		class_datum_t *c = pdb.class_val_to_struct[i];
		if (!c) continue;
		for (constraint_node_t *cn = c->constraints; cn; cn = cn->next)
			for (constraint_expr_t *e = cn->expr; e; e = e->next) {
				if (e->expr_type != CEXPR_NAMES || !(e->attr & CEXPR_TYPE) || !(e->attr & CEXPR_TARGET)) continue;
				if (!e->type_names || !ebitmap_get_bit(&e->type_names->types, attr->s.value - 1)) continue;
				if (ebitmap_set_bit(&e->names, t->s.value - 1, 1)) return -1;
				hit++;
			}
	}
	if (!hit) { fprintf(stderr, "sepatch: 约束里没找到 mlstrustedobject\n"); return -1; }
	// 属性成员表也补上（getattr 之类按属性查的地方一致）
	if (pdb.type_attr_map) ebitmap_set_bit(&pdb.type_attr_map[t->s.value - 1], attr->s.value - 1, 1);
	if (pdb.attr_type_map) ebitmap_set_bit(&pdb.attr_type_map[attr->s.value - 1], t->s.value - 1, 1);
	return 0;
}

int main(int argc, char **argv) {
	if (argc != 4) { fprintf(stderr, "用法：sepatch <输入 policy> <输出 policy> <规则文件>\n"); return 2; }
	sepol_set_policydb(&pdb);
	FILE *in = fopen(argv[1], "rb");
	if (!in) { perror(argv[1]); return 1; }
	struct policy_file pf;
	policy_file_init(&pf);
	pf.type = PF_USE_STDIO;
	pf.fp = in;
	if (policydb_init(&pdb) || policydb_read(&pdb, &pf, 0)) { fprintf(stderr, "sepatch: 读不了规则\n"); return 1; }
	fclose(in);

	FILE *rf = fopen(argv[3], "r");
	if (!rf) { perror(argv[3]); return 1; }
	char line[512];
	int n = 0, bad = 0;
	while (fgets(line, sizeof line, rf)) {
		char *w[6] = {0};
		int k = 0;
		for (char *p = strtok(line, " \t\r\n"); p && k < 6; p = strtok(NULL, " \t\r\n")) w[k++] = p;
		if (k == 0 || w[0][0] == '#') continue;
		if (!strcmp(w[0], "permissive") && k == 2) {
			type_datum_t *t = type_of(w[1]);
			if (!t || ebitmap_set_bit(&pdb.permissive_map, t->s.value, 1)) { bad++; continue; }
		} else if (!strcmp(w[0], "mlstrusted") && k == 2) {
			if (add_mlstrusted(w[1])) { bad++; continue; }
		} else if (!strcmp(w[0], "allow") && k == 5) {
			if (add_allow(w[1], w[2], w[3], w[4])) { bad++; continue; }
		} else {
			fprintf(stderr, "sepatch: 看不懂：%s\n", w[0]);
			bad++;
			continue;
		}
		n++;
	}
	fclose(rf);
	if (bad) { fprintf(stderr, "sepatch: %d 条规则不对，不写\n", bad); return 1; }

	FILE *out = fopen(argv[2], "wb");
	if (!out) { perror(argv[2]); return 1; }
	policy_file_init(&pf);
	pf.type = PF_USE_STDIO;
	pf.fp = out;
	if (policydb_write(&pdb, &pf)) { fprintf(stderr, "sepatch: 写规则失败\n"); return 1; }
	fclose(out);
	printf("sepatch：加了 %d 条\n", n);
	return 0;
}
